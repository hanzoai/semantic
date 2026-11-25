package resonance

import (
	"fmt"
	"math"
	"sort"
	"testing"
	"time"
)

// splitmix64 provides a deterministic PRNG for reproducible quality and latency benchmarks.
type splitmix64 uint64

func (s *splitmix64) next() uint64 {
	*s += 0x9e3779b97f4a7c15
	z := uint64(*s)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (s *splitmix64) float64() float64 {
	return float64(s.next()>>11) / (1 << 53)
}

func (s *splitmix64) unitVec(dim int) []float32 {
	v := make([]float32, dim)
	var sum float64
	for i := range v {
		x := s.float64()*2 - 1
		v[i] = float32(x)
		sum += float64(v[i]) * float64(v[i])
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}

func cosineBaseline(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	c := dot / math.Sqrt(na*nb)
	return math.Max(-1, math.Min(1, c))
}

func blendUnitVec(base, noise []float32, targetCos float64) []float32 {
	// Gram-Schmidt orthogonalization of noise against unit vector base.
	var proj float64
	for i := range base {
		proj += float64(base[i]) * float64(noise[i])
	}
	orth := make([]float64, len(base))
	var norm2 float64
	for i := range base {
		o := float64(noise[i]) - proj*float64(base[i])
		orth[i] = o
		norm2 += o * o
	}
	invOrth := 1.0 / math.Sqrt(norm2)
	sinT := math.Sqrt(math.Max(0, 1-targetCos*targetCos))
	out := make([]float32, len(base))
	var outNorm2 float64
	for i := range base {
		val := targetCos*float64(base[i]) + sinT*(orth[i]*invOrth)
		out[i] = float32(val)
		outNorm2 += float64(out[i]) * float64(out[i])
	}
	invOut := float32(1.0 / math.Sqrt(outNorm2))
	for i := range out {
		out[i] *= invOut
	}
	return out
}

// TestCrossLanguageParityVectors locks deterministic numerical test vectors shared
// with hanzoai/engine's hanzo_engine::resonance module (tolerance <= 1e-6).
func TestCrossLanguageParityVectors(t *testing.T) {
	a, err := Cartesian([]float32{0.6, -0.3, 0.5, 0.2}, []float32{0.1, 0.4, -0.2, 0.3})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Cartesian([]float32{0.5, -0.2, 0.4, 0.1}, []float32{0.2, 0.3, -0.1, 0.4})
	if err != nil {
		t.Fatal(err)
	}
	gotAB, err := Score(a, b)
	if err != nil {
		t.Fatal(err)
	}
	// Exact analytical value:
	// E_a = 0.36+0.09+0.25+0.04 + 0.01+0.16+0.04+0.09 = 1.04
	// E_b = 0.25+0.04+0.16+0.01 + 0.04+0.09+0.01+0.16 = 0.76
	// dot = 0.30+0.06+0.20+0.02 + 0.02+0.12+0.02+0.12 = 0.86
	// total = 1.80, interference = (1.80 + 1.72)/1.80 = 3.52/1.80
	// balance = 2*sqrt(1.04*0.76)/1.80
	// score = 0.5 * interference * balance = 0.965875429...
	closeTo(t, gotAB, 0.965875429)

	gotNeg, err := Score(a, b.Negate())
	if err != nil {
		t.Fatal(err)
	}
	// interference = (1.80 - 1.72)/1.80 = 0.08/1.80
	// score = 0.5 * (0.08/1.80) * balance = 0.021951715...
	closeTo(t, gotNeg, 0.021951715)

	p1, err := Polar([]float32{1.0, 2.0, 0.5, 1.5}, []float32{0.25, -0.75, 1.1, -0.4})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Polar([]float32{1.2, 1.8, 0.7, 1.3}, []float32{0.35, -0.55, 0.9, -0.1})
	if err != nil {
		t.Fatal(err)
	}
	gotPolar, err := Score(p1, p2)
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, gotPolar, 0.981487148)

	hits, err := Rank(a, []Candidate{
		{ID: "negated", Wave: b.Negate(), Cosine: 0.95},
		{ID: "aligned", Wave: b, Cosine: 0.80},
		{ID: "orthogonal", Wave: mustEmbed(t, []float32{0, 1, 0, 0}), Cosine: 0.50},
	}, 0.75, 3)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].ID != "aligned" || hits[1].ID != "orthogonal" || hits[2].ID != "negated" {
		t.Fatalf("unexpected rank order: %+v", hits)
	}
	closeTo(t, hits[0].Score, 0.75*0.965875429+0.25*0.90)
	closeTo(t, hits[2].Score, 0.75*0.021951715+0.25*0.975)
}

// TestContrastiveEvaluationReport runs a reproducible 500-query contrastive polarity
// shortlist benchmark comparing:
//   - dense cosine baseline
//   - resonance without phase metadata (verifies zero-regression parity)
//   - resonance with grounded polarity phase (w=0.50, w=0.75)
//   - structured metadata polarity filter baseline (honest comparison per README)
//
// Run with: GOWORK=off go test -v -run TestContrastiveEvaluationReport ./resonance
func TestContrastiveEvaluationReport(t *testing.T) {
	const (
		numQueries    = 500
		shortlistSize = 50
		dim           = 384
	)
	rng := splitmix64(20261125)

	type evalMetrics struct {
		recall1         float64
		mrr             float64
		ndcg10          float64
		contradictionFP float64
		totalDur        time.Duration
	}

	var mCos, mResNoPhase, mRes50, mRes75, mMetaFilter evalMetrics

	scoreMetrics := func(m *evalMetrics, ids []string) {
		if len(ids) == 0 {
			return
		}
		if ids[0] == "target" {
			m.recall1++
		}
		if ids[0] == "negated" {
			m.contradictionFP++
		}
		for idx, id := range ids {
			if id == "target" {
				m.mrr += 1.0 / float64(idx+1)
				if idx < 10 {
					m.ndcg10 += 1.0 / math.Log2(float64(idx+2))
				}
				break
			}
		}
	}

	for q := 0; q < numQueries; q++ {
		qVec := rng.unitVec(dim)
		qWave, err := Embed(qVec)
		if err != nil {
			t.Fatal(err)
		}

		// Target has slightly lower raw cosine (0.86-0.91) than its negated twin (0.93-0.98)
		// to test hard lexical-overlap contrastive pairs where embeddings confuse polarity.
		targetCos := 0.86 + 0.05*rng.float64()
		negCos := 0.93 + 0.05*rng.float64()

		targetVec := blendUnitVec(qVec, rng.unitVec(dim), targetCos)
		negVec := blendUnitVec(qVec, rng.unitVec(dim), negCos)

		targetWave, _ := Embed(targetVec)
		negWaveNoPhase, _ := Embed(negVec)
		negWavePhased := negWaveNoPhase.Negate()

		type rawCand struct {
			id       string
			vec      []float32
			cos      float64
			polarity int // +1 asserted, -1 negated, 0 neutral/other
		}
		raw := make([]rawCand, 0, shortlistSize)
		raw = append(raw,
			rawCand{id: "target", vec: targetVec, cos: cosineBaseline(qVec, targetVec), polarity: +1},
			rawCand{id: "negated", vec: negVec, cos: cosineBaseline(qVec, negVec), polarity: -1},
		)
		for d := 2; d < shortlistSize; d++ {
			c := 0.25 + 0.55*rng.float64()
			dv := blendUnitVec(qVec, rng.unitVec(dim), c)
			raw = append(raw, rawCand{
				id:       fmt.Sprintf("dist-%02d", d),
				vec:      dv,
				cos:      cosineBaseline(qVec, dv),
				polarity: 0,
			})
		}

		candsNoPhase := make([]Candidate, shortlistSize)
		candsPhased := make([]Candidate, shortlistSize)
		for i, rc := range raw {
			w, _ := Embed(rc.vec)
			candsNoPhase[i] = Candidate{ID: rc.id, Wave: w, Cosine: rc.cos}
			if rc.polarity == -1 {
				candsPhased[i] = Candidate{ID: rc.id, Wave: negWavePhased, Cosine: rc.cos}
			} else if rc.id == "target" {
				candsPhased[i] = Candidate{ID: rc.id, Wave: targetWave, Cosine: rc.cos}
			} else {
				candsPhased[i] = Candidate{ID: rc.id, Wave: w, Cosine: rc.cos}
			}
		}

		// Arm A: Dense cosine baseline
		t0 := time.Now()
		cosOrder := make([]rawCand, len(raw))
		copy(cosOrder, raw)
		sort.Slice(cosOrder, func(i, j int) bool {
			if cosOrder[i].cos != cosOrder[j].cos {
				return cosOrder[i].cos > cosOrder[j].cos
			}
			return cosOrder[i].id < cosOrder[j].id
		})
		mCos.totalDur += time.Since(t0)
		idsCos := make([]string, 10)
		for i := range idsCos {
			idsCos[i] = cosOrder[i].id
		}
		scoreMetrics(&mCos, idsCos)

		// Arm B: Resonance without grounded phase (zero imaginary part, weight=0.75)
		t0 = time.Now()
		hitsNoPhase, err := Rank(qWave, candsNoPhase, 0.75, 10)
		if err != nil {
			t.Fatal(err)
		}
		mResNoPhase.totalDur += time.Since(t0)
		idsNoPhase := make([]string, len(hitsNoPhase))
		for i, h := range hitsNoPhase {
			idsNoPhase[i] = h.ID
		}
		scoreMetrics(&mResNoPhase, idsNoPhase)
		// Equal-norm real vectors must preserve cosine order (zero regression).
		if idsNoPhase[0] != idsCos[0] {
			t.Fatalf("query %d: real-only resonance diverged from cosine: %s vs %s", q, idsNoPhase[0], idsCos[0])
		}

		// Arm C1: Resonance with grounded polarity phase (weight=0.50)
		t0 = time.Now()
		hits50, err := Rank(qWave, candsPhased, 0.50, 10)
		if err != nil {
			t.Fatal(err)
		}
		mRes50.totalDur += time.Since(t0)
		ids50 := make([]string, len(hits50))
		for i, h := range hits50 {
			ids50[i] = h.ID
		}
		scoreMetrics(&mRes50, ids50)

		// Arm C2: Resonance with grounded polarity phase (weight=0.75)
		t0 = time.Now()
		hits75, err := Rank(qWave, candsPhased, 0.75, 10)
		if err != nil {
			t.Fatal(err)
		}
		mRes75.totalDur += time.Since(t0)
		ids75 := make([]string, len(hits75))
		for i, h := range hits75 {
			ids75[i] = h.ID
		}
		scoreMetrics(&mRes75, ids75)

		// Arm D: Structured metadata polarity filter baseline (excludes polarity == -1)
		t0 = time.Now()
		filtered := make([]rawCand, 0, len(raw))
		for _, rc := range raw {
			if rc.polarity != -1 {
				filtered = append(filtered, rc)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].cos != filtered[j].cos {
				return filtered[i].cos > filtered[j].cos
			}
			return filtered[i].id < filtered[j].id
		})
		mMetaFilter.totalDur += time.Since(t0)
		idsMeta := make([]string, 10)
		for i := range idsMeta {
			idsMeta[i] = filtered[i].id
		}
		scoreMetrics(&mMetaFilter, idsMeta)
	}

	norm := func(m evalMetrics) (float64, float64, float64, float64, float64) {
		n := float64(numQueries)
		return m.recall1 / n, m.mrr / n, m.ndcg10 / n, m.contradictionFP / n, float64(m.totalDur.Microseconds()) / n
	}

	printRow := func(name string, m evalMetrics) {
		r1, mrr, ndcg, fp, us := norm(m)
		t.Logf("%-36s | R@1=%.4f | MRR=%.4f | nDCG@10=%.4f | ContraFP=%.4f | avg=%.2f us/query",
			name, r1, mrr, ndcg, fp, us)
	}
	printRow("A. Dense cosine baseline", mCos)
	printRow("B. Resonance (no phase, w=0.75)", mResNoPhase)
	printRow("C1. Resonance (grounded phase, 0.50)", mRes50)
	printRow("C2. Resonance (grounded phase, 0.75)", mRes75)
	printRow("D. Structured polarity filter", mMetaFilter)

	// Guardrails:
	// 1. Without grounded phase, resonance matches dense cosine exactly.
	if mResNoPhase.recall1 != mCos.recall1 || mResNoPhase.contradictionFP != mCos.contradictionFP {
		t.Fatalf("no-phase resonance must match dense cosine baseline")
	}
	// 2. With grounded phase metadata, resonance beats dense cosine on hard polarity pairs.
	if mRes75.recall1 <= mCos.recall1 || mRes75.contradictionFP >= mCos.contradictionFP {
		t.Fatalf("phased resonance did not improve over dense cosine baseline")
	}
	// 3. Honest bound: structured polarity filter matches binary-negation Recall@1 too.
	if mMetaFilter.recall1 != mRes75.recall1 {
		t.Logf("note: structured filter R@1=%.4f vs resonance(0.75) R@1=%.4f",
			mMetaFilter.recall1/numQueries, mRes75.recall1/numQueries)
	}
}

func makeBenchWaves(dim, n int) (Wave, []float32, []Candidate, [][]float32) {
	rng := splitmix64(42 + uint64(dim)*1000 + uint64(n))
	qVec := rng.unitVec(dim)
	qWave, _ := Embed(qVec)
	cands := make([]Candidate, n)
	raw := make([][]float32, n)
	for i := 0; i < n; i++ {
		v := rng.unitVec(dim)
		raw[i] = v
		w, _ := Embed(v)
		if i%5 == 0 {
			w = w.Negate()
		}
		cands[i] = Candidate{ID: fmt.Sprintf("doc-%06d", i), Wave: w, Cosine: cosineBaseline(qVec, v)}
	}
	return qWave, qVec, cands, raw
}

var benchSink float64
var benchHitsSink []Hit

func benchmarkPairwise(b *testing.B, dim int, useResonance bool) {
	qWave, qVec, cands, raw := makeBenchWaves(dim, 1)
	b.ResetTimer()
	var acc float64
	if useResonance {
		target := cands[0].Wave
		for i := 0; i < b.N; i++ {
			s, _ := Score(qWave, target)
			acc += s
		}
	} else {
		target := raw[0]
		for i := 0; i < b.N; i++ {
			acc += cosineBaseline(qVec, target)
		}
	}
	benchSink = acc
}

func BenchmarkCosineBaseline_D384(b *testing.B)  { benchmarkPairwise(b, 384, false) }
func BenchmarkResonanceScore_D384(b *testing.B)  { benchmarkPairwise(b, 384, true) }
func BenchmarkCosineBaseline_D768(b *testing.B)  { benchmarkPairwise(b, 768, false) }
func BenchmarkResonanceScore_D768(b *testing.B)  { benchmarkPairwise(b, 768, true) }
func BenchmarkCosineBaseline_D1024(b *testing.B) { benchmarkPairwise(b, 1024, false) }
func BenchmarkResonanceScore_D1024(b *testing.B) { benchmarkPairwise(b, 1024, true) }

func benchmarkRank(b *testing.B, dim, n int) {
	qWave, _, cands, _ := makeBenchWaves(dim, n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hits, err := Rank(qWave, cands, 0.75, 10)
		if err != nil {
			b.Fatal(err)
		}
		benchHitsSink = hits
	}
}

func BenchmarkRank_D384_N50(b *testing.B)     { benchmarkRank(b, 384, 50) }
func BenchmarkRank_D384_N200(b *testing.B)    { benchmarkRank(b, 384, 200) }
func BenchmarkRank_D768_N50(b *testing.B)     { benchmarkRank(b, 768, 50) }
func BenchmarkRank_D768_N200(b *testing.B)    { benchmarkRank(b, 768, 200) }
func BenchmarkRank_D1024_N50(b *testing.B)    { benchmarkRank(b, 1024, 50) }
func BenchmarkRank_D1024_N200(b *testing.B)   { benchmarkRank(b, 1024, 200) }
func BenchmarkRank_D1024_N10000(b *testing.B) { benchmarkRank(b, 1024, 10000) }
