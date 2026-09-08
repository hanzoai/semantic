package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func lending() Policy {
	return Policy{
		ID:    "p1",
		Name:  "lending",
		Topic: "loan",
		Rules: map[string]any{"min_credit_score": 650, "max_debt_ratio": 0.4},
		At:    at(1),
	}
}

func applicant(score int, ratio float64) Decision {
	d := Decision{
		Topic:      "loan",
		Case:       "credit application",
		Reason:     "the file supports it",
		Confidence: 0.9,
		Props:      map[string]any{"credit_score": score, "debt_ratio": ratio},
	}
	d.ID, d.Choice = "d1", "approved"
	return d
}

func TestAddChecksWhatAPolicySays(t *testing.T) {
	for _, c := range []struct {
		name string
		in   Policy
	}{
		{"no name", Policy{Rules: map[string]any{"min_score": 1}}},
		{"no rules", Policy{Name: "empty"}},
		{"a bound that is not a number", Policy{Name: "x", Rules: map[string]any{"min_score": "high"}}},
		{"a ratio outside 0 to 1", Policy{Name: "x", Rules: map[string]any{"max_debt_ratio": 1.5}}},
		{"a membership test with nothing to be a member of", Policy{Name: "x", Rules: map[string]any{"one_choice": nil}}},
		{"a version that is not one", Policy{Name: "x", Version: "one", Rules: map[string]any{"min_score": 1}}},
	} {
		if _, err := (&Rules{}).Add(c.in); err == nil {
			t.Errorf("%s: Add accepted it", c.name)
		}
	}

	r := &Rules{}
	p, err := r.Add(Policy{Name: "lending", Rules: map[string]any{"min_credit_score": 650}})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if p.ID == "" || p.Version != "1.0" || p.At.IsZero() {
		t.Errorf("wrote %+v, want an id, a first version and a time filled in", p)
	}
	if _, err := r.Add(p); err == nil {
		t.Error("the same id and version was written twice")
	}
}

func TestVersionsAreKept(t *testing.T) {
	r := &Rules{}
	r.Add(lending())
	next, err := r.Revise("p1", map[string]any{"min_credit_score": 700})
	if err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if next.Version != "1.1" {
		t.Errorf("the new version is %q, want 1.1", next.Version)
	}
	if p, _ := r.Get("p1", ""); p.Version != "1.1" {
		t.Errorf("the newest version is %q, want 1.1", p.Version)
	}
	old, ok := r.Get("p1", "1.0")
	if !ok || old.Rules["min_credit_score"] != 650 {
		t.Errorf("the version it replaced reads %+v; a decision taken under it must stay explainable", old)
	}
	if got := len(r.History("p1")); got != 2 {
		t.Errorf("%d versions kept, want 2", got)
	}
	if _, err := r.Revise("nobody", nil); err == nil {
		t.Error("revised a policy that is not there")
	}
	if _, ok := r.Get("p1", "9.9"); ok {
		t.Error("read a version that was never written")
	}
}

func TestBump(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"1.0", "1.1"},
		{"1.9", "1.10"},
		{"2.3.4", "2.3.5"},
		{"1.0-beta", "1.0-beta.1"},
	} {
		if got := bump(c.in); got != c.want {
			t.Errorf("bump(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestForNarrowsByTopic(t *testing.T) {
	r := &Rules{}
	r.Add(lending())
	r.Add(Policy{ID: "p2", Name: "every decision", Rules: map[string]any{"min_confidence": 0.5}})
	r.Add(Policy{ID: "p3", Name: "triage", Topic: "medical", Rules: map[string]any{"min_confidence": 0.9}})

	if got := policyIDs(r.For("loan")); !reflect.DeepEqual(got, []string{"p1", "p2"}) {
		t.Errorf("loan is governed by %v, want the lending policy and the one with no topic", got)
	}
	if got := policyIDs(r.For("medical")); !reflect.DeepEqual(got, []string{"p2", "p3"}) {
		t.Errorf("medical is governed by %v, want p2 and p3", got)
	}
	if !r.Drop("p1") {
		t.Fatal("Drop reported nothing to drop")
	}
	if r.Drop("p1") {
		t.Error("dropping twice reported a second drop")
	}
	if got := policyIDs(r.For("loan")); !reflect.DeepEqual(got, []string{"p2"}) {
		t.Errorf("after the drop loan is governed by %v, want p2", got)
	}
}

// The port of test_evaluate_compliance_numeric_rules, _string_rules and
// _list_rules, plus the field lookup that finds verification_status when the
// rule says status.
func TestCheck(t *testing.T) {
	r := &Rules{}
	for _, c := range []struct {
		name  string
		rules map[string]any
		props map[string]any
		want  bool
		why   string
	}{
		{
			name:  "numbers inside their bounds",
			rules: map[string]any{"min_credit_score": 650, "max_debt_ratio": 0.4},
			props: map[string]any{"credit_score": 700, "debt_ratio": 0.3},
			want:  true,
		},
		{
			name:  "a number under its bound",
			rules: map[string]any{"min_credit_score": 650},
			props: map[string]any{"credit_score": 600},
			want:  false,
			why:   "credit_score is 600, under 650",
		},
		{
			name:  "a number over its bound",
			rules: map[string]any{"max_debt_ratio": 0.4},
			props: map[string]any{"debt_ratio": 0.9},
			want:  false,
			why:   "debt_ratio is 0.9, over 0.4",
		},
		{
			name:  "strings, found by the end of a longer name",
			rules: map[string]any{"has_status": "passed", "max_risk_level": "medium"},
			props: map[string]any{"verification_status": "passed", "risk_level": "low"},
			want:  true,
		},
		{
			name:  "a string over its bound, read as letters",
			rules: map[string]any{"max_risk_level": "medium"},
			props: map[string]any{"risk_level": "severe"},
			want:  false,
			why:   "risk_level is severe, over medium",
		},
		{
			name:  "a list holding everything asked for",
			rules: map[string]any{"has_documents": []string{"income", "employment"}},
			props: map[string]any{"submitted_documents": []string{"income", "employment", "id"}},
			want:  true,
		},
		{
			name:  "a list missing one",
			rules: map[string]any{"has_documents": []string{"income", "employment"}},
			props: map[string]any{"submitted_documents": []string{"income"}},
			want:  false,
			why:   "documents lacks employment",
		},
		{
			name:  "one of a set",
			rules: map[string]any{"one_choice": []string{"approved", "refused"}},
			props: nil,
			want:  true,
		},
		{
			name:  "not one of a set",
			rules: map[string]any{"one_choice": []string{"refused", "held"}},
			props: nil,
			want:  false,
			why:   "choice is approved, not one of",
		},
		{
			name:  "a rule about a field the decision does not carry",
			rules: map[string]any{"min_credit_score": 650},
			props: map[string]any{"something_else": 1},
			want:  false,
			why:   "credit_score is missing",
		},
		{
			name:  "a bare name asks only that the field be there",
			rules: map[string]any{"appraisal": true},
			props: map[string]any{"appraisal": "done by a surveyor"},
			want:  true,
		},
		{
			name:  "the decision's own fields are readable too",
			rules: map[string]any{"min_confidence": 0.8},
			props: nil,
			want:  true,
		},
		{
			name:  "and can fail",
			rules: map[string]any{"min_confidence": 0.95},
			props: nil,
			want:  false,
			why:   "confidence is 0.9, under 0.95",
		},
	} {
		d := applicant(700, 0.3)
		d.Props = c.props
		v := r.Check(d, Policy{ID: "p1", Name: "lending", Version: "1.0", Rules: c.rules})
		if v.OK != c.want {
			t.Errorf("%s: passed = %v, want %v (%s)", c.name, v.OK, c.want, v.Why)
			continue
		}
		if !c.want && !strings.Contains(v.Why, c.why) {
			t.Errorf("%s: refused because %q, want it to say %q", c.name, v.Why, c.why)
		}
		if v.Policy != "p1" {
			t.Errorf("%s: the verdict names %q, want the policy that gave it", c.name, v.Policy)
		}
	}
}

func TestVetTakesTheFirstRefusal(t *testing.T) {
	r := &Rules{}
	r.Add(lending())
	r.Add(Policy{ID: "p2", Name: "confidence", Topic: "loan", Rules: map[string]any{"min_confidence": 0.95}})

	v := r.Vet(applicant(700, 0.3))
	if v.OK {
		t.Fatalf("the decision passed; the second policy wants more confidence than it has")
	}
	if v.Policy != "p2" {
		t.Errorf("the verdict names %q, want the policy that refused", v.Policy)
	}
	if !strings.Contains(v.Why, "confidence is 0.9, under 0.95") {
		t.Errorf("the reason reads %q, want the rule it failed", v.Why)
	}
	if v := r.Vet(Decision{Topic: "nothing governs this"}); !v.OK || !strings.Contains(v.Why, "no policy") {
		t.Errorf("an ungoverned topic gave %+v, want it allowed and said so", v)
	}
	r.Drop("p2")
	if v := r.Vet(applicant(700, 0.3)); !v.OK || !strings.Contains(v.Why, "lending 1.0") {
		t.Errorf("a passing decision gave %+v, want the policies it passed named", v)
	}
}

func TestWaiverLetsOneDecisionPast(t *testing.T) {
	r := &Rules{}
	r.Add(lending())
	refused := applicant(600, 0.3)
	if v := r.Vet(refused); v.OK {
		t.Fatal("the decision passed before any waiver")
	}
	for _, w := range []Waiver{
		{Policy: "p1", By: "manager"},
		{Decision: "d1", By: "manager"},
		{Decision: "d1", Policy: "p1"},
	} {
		if err := r.Waive(w); err == nil {
			t.Errorf("a waiver missing something was accepted: %+v", w)
		}
	}
	if err := r.Waive(Waiver{Decision: "d1", Policy: "p1", By: "manager", Why: "long standing customer"}); err != nil {
		t.Fatalf("Waive: %v", err)
	}
	v := r.Vet(refused)
	if !v.OK {
		t.Fatalf("the waived decision is still refused: %s", v.Why)
	}
	if !strings.Contains(v.Why, "manager") || !strings.Contains(v.Why, "long standing customer") {
		t.Errorf("the reason reads %q, want whose authority and why", v.Why)
	}
	if w := r.Waivers(); len(w) != 1 || w[0].At.IsZero() {
		t.Errorf("waivers are %+v, want one, with the time it was granted", w)
	}
	other := applicant(600, 0.3)
	other.ID = "d2"
	if v := r.Vet(other); v.OK {
		t.Error("the waiver let another decision past as well")
	}
}

func TestAllowIsTheCoarseQuestion(t *testing.T) {
	ctx := context.Background()
	r := &Rules{}
	r.Add(Policy{ID: "p1", Name: "outcomes", Rules: map[string]any{"one_choice": []string{"approved", "refused"}}})
	r.Add(Policy{ID: "p2", Name: "confidence", Rules: map[string]any{"min_confidence": 0.9}})

	if ok, why := r.Allow(ctx, "ada", "approved"); !ok {
		t.Errorf("a listed choice was refused: %s", why)
	}
	ok, why := r.Allow(ctx, "ada", "ignored the file")
	if ok {
		t.Error("a choice no policy lists was allowed")
	}
	if !strings.Contains(why, "not one of") {
		t.Errorf("the reason reads %q, want the rule it failed", why)
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	if ok, _ := r.Allow(stopped, "ada", "approved"); ok {
		t.Error("a cancelled question was still answered")
	}
}

// The port of _calculate_impact_metrics: what a rule change would cost.
func TestSway(t *testing.T) {
	r := &Rules{}
	r.Add(lending())
	ds := []Decision{applicant(700, 0.3), applicant(660, 0.3), applicant(800, 0.3)}
	ds[1].ID, ds[2].ID = "d2", "d3"

	s, err := r.Sway("p1", map[string]any{"min_credit_score": 750, "max_debt_ratio": 0.4}, ds)
	if err != nil {
		t.Fatalf("Sway: %v", err)
	}
	if s.Judged != 3 || s.Affected != 2 {
		t.Errorf("judged %d and affected %d, want 3 and 2: only the 800 still passes", s.Judged, s.Affected)
	}
	almost(t, "the fall in what passes", s.Fall, 1.0/3.0-1)
	almost(t, "the added risk", s.Risk, (1-1.0/3.0)*0.5)
	if want := (Swap{Was: 650, Now: 750}); s.Changed["min_credit_score"] != want {
		t.Errorf("the change is %+v, want %+v", s.Changed["min_credit_score"], want)
	}
	if _, ok := s.Changed["max_debt_ratio"]; ok {
		t.Errorf("a rule that did not change is listed as changed: %v", s.Changed)
	}
	if _, err := r.Sway("nobody", nil, ds); err == nil {
		t.Error("measured a change to a policy that is not there")
	}
	empty, _ := r.Sway("p1", map[string]any{"min_credit_score": 750}, nil)
	if empty.Fall != 0 || empty.Risk != 0 {
		t.Errorf("with nothing judged the cost is %+v, want zero", empty)
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b any
		want int
		ok   bool
	}{
		{1, 2, -1, true},
		{2.5, 2.5, 0, true},
		{3, 2.5, 1, true},
		{"a", "b", -1, true},
		{"medium", "medium", 0, true},
		{1, "two", 0, false},
		{"one", 2, 0, false},
		{[]string{"a"}, "a", 0, false},
	} {
		got, ok := compare(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("compare(%v, %v) = %d, %v, want %d, %v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

func policyIDs(ps []Policy) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}
