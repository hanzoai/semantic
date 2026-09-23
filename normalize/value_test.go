package normalize

import (
	"errors"
	"math/big"
	"testing"
	"time"
	_ "time/tzdata"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestDate(t *testing.T) {
	york, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	plus9 := time.FixedZone("", 9*3600)
	for _, c := range []struct {
		in          string
		a           Anchor
		from, until time.Time
		grain       Grain
		canon       string
	}{
		{"2023", Anchor{}, day(2023, 1, 1), day(2024, 1, 1), Year, "2023"},
		{"2023-03", Anchor{}, day(2023, 3, 1), day(2023, 4, 1), Month, "2023-03"},
		{"March 2023", Anchor{}, day(2023, 3, 1), day(2023, 4, 1), Month, "2023-03"},
		{"Mar. 2023", Anchor{}, day(2023, 3, 1), day(2023, 4, 1), Month, "2023-03"},
		{"03/2023", Anchor{}, day(2023, 3, 1), day(2023, 4, 1), Month, "2023-03"},
		{"2023-03-01", Anchor{}, day(2023, 3, 1), day(2023, 3, 2), Day, "2023-03-01"},
		{"1 March 2023", Anchor{}, day(2023, 3, 1), day(2023, 3, 2), Day, "2023-03-01"},
		{"March 1st, 2023", Anchor{}, day(2023, 3, 1), day(2023, 3, 2), Day, "2023-03-01"},
		{"2023/03/01", Anchor{}, day(2023, 3, 1), day(2023, 3, 2), Day, "2023-03-01"},
		{"01/03/2023", Anchor{Order: DMY}, day(2023, 3, 1), day(2023, 3, 2), Day, "2023-03-01"},
		{"03/01/2023", Anchor{Order: MDY}, day(2023, 3, 1), day(2023, 3, 2), Day, "2023-03-01"},
		{"29.02.2024", Anchor{Order: DMY}, day(2024, 2, 29), day(2024, 3, 1), Day, "2024-02-29"},
		{"yesterday", Anchor{At: day(2024, 3, 1)}, day(2024, 2, 29), day(2024, 3, 1), Day, "2024-02-29"},
		{"Tomorrow", Anchor{At: day(2023, 12, 31)}, day(2024, 1, 1), day(2024, 1, 2), Day, "2024-01-01"},
		{
			"2023-03-01T10:00Z", Anchor{},
			time.Date(2023, 3, 1, 10, 0, 0, 0, time.UTC), time.Date(2023, 3, 1, 10, 1, 0, 0, time.UTC),
			Minute, "2023-03-01T10:00Z",
		},
		{
			"2023-03-01T10:00:00.5+09:00", Anchor{},
			time.Date(2023, 3, 1, 10, 0, 0, 5e8, plus9), time.Date(2023, 3, 1, 10, 0, 0, 5e8+1, plus9),
			Nano, "2023-03-01T10:00:00.5+09:00",
		},
		{
			"2023-07-01 09:30:15", Anchor{Zone: york},
			time.Date(2023, 7, 1, 9, 30, 15, 0, york), time.Date(2023, 7, 1, 9, 30, 16, 0, york),
			Second, "2023-07-01T09:30:15-04:00",
		},
		{
			"2023-03-01T00:00:00Z/2023-03-01T12:00:00Z", Anchor{},
			time.Date(2023, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2023, 3, 1, 12, 0, 0, 0, time.UTC),
			0, "2023-03-01T00:00:00Z/2023-03-01T12:00:00Z",
		},
	} {
		got, err := Date.Parse(c.a, c.in)
		if err != nil {
			t.Errorf("Date(%q): %v", c.in, err)
			continue
		}
		if !got.From.Equal(c.from) || !got.Until.Equal(c.until) || got.Grain != c.grain {
			t.Errorf("Date(%q) = [%v, %v) grain %d, want [%v, %v) grain %d",
				c.in, got.From, got.Until, got.Grain, c.from, c.until, c.grain)
		}
		if s := Date.Canon(got); s != c.canon {
			t.Errorf("Canon(Date(%q)) = %q, want %q", c.in, s, c.canon)
		}
	}
}

// TestRefuse holds every kind to refusing what it would have to guess, and to
// saying which: ErrAmbiguous for text with two readings the Anchor does not
// choose between, ErrSyntax for text that is not a value of the kind.
func TestRefuse(t *testing.T) {
	york, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		kind Datatype
		in   string
		a    Anchor
		want error
	}{
		{Date, "01/03/2023", Anchor{}, ErrAmbiguous},
		{Date, "01/03/23", Anchor{Order: DMY}, ErrAmbiguous},
		{Date, "March 1", Anchor{}, ErrAmbiguous},
		{Date, "2023-03-01T10:00", Anchor{}, ErrAmbiguous},
		{Date, "2023-11-05T01:30", Anchor{Zone: york}, ErrAmbiguous},
		{Date, "yesterday", Anchor{}, ErrAmbiguous},
		{Date, "2023-03-12T02:30", Anchor{Zone: york}, ErrSyntax},
		{Date, "2023-02-29", Anchor{}, ErrSyntax},
		{Date, "31/04/2023", Anchor{Order: DMY}, ErrSyntax},
		{Date, "01/03-2023", Anchor{Order: DMY}, ErrSyntax},
		{Date, "Smarch 2023", Anchor{}, ErrSyntax},
		{Date, "2023-03-01T12:00:00Z/2023-03-01T00:00:00Z", Anchor{}, ErrSyntax},

		{Number, "1,234", Anchor{}, ErrAmbiguous},
		{Number, "1.234", Anchor{}, ErrAmbiguous},
		{Number, "-12,345", Anchor{}, ErrAmbiguous},
		{Number, "1,23,456", Anchor{Point: '.'}, ErrSyntax},
		{Number, "1,234.5.6", Anchor{}, ErrSyntax},
		{Number, "1,234 567", Anchor{Point: '.'}, ErrSyntax},
		{Number, "12a", Anchor{}, ErrSyntax},
		{Number, "1/0", Anchor{}, ErrSyntax},
		{Number, "", Anchor{}, ErrSyntax},

		{Quantity, "1,500 kg", Anchor{}, ErrAmbiguous},
		{Quantity, "5", Anchor{}, ErrSyntax},
		{Quantity, "kg", Anchor{}, ErrSyntax},

		{Money, "¥500", Anchor{}, ErrAmbiguous},
		{Money, "$5", Anchor{}, ErrAmbiguous},
		{Money, "£1.50", Anchor{}, ErrAmbiguous},
		{Money, "-£ 0", Anchor{}, ErrAmbiguous},
		{Money, "100 kr", Anchor{}, ErrAmbiguous},
		{Money, "EUR 1.500", Anchor{}, ErrAmbiguous},
		{Money, "all 5", Anchor{}, ErrSyntax},
		{Money, "500", Anchor{}, ErrSyntax},
		{Money, "-USD -5", Anchor{}, ErrSyntax},
	} {
		got, err := c.kind.Norm(c.a, c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("%v(%q) = %q, %v; want %v", c.kind, c.in, got, err, c.want)
		}
	}
}

func TestNumber(t *testing.T) {
	for _, c := range []struct {
		in    string
		a     Anchor
		want  *big.Rat
		canon string
	}{
		{"42", Anchor{}, big.NewRat(42, 1), "42"},
		{"-0.50", Anchor{}, big.NewRat(-1, 2), "-0.5"},
		{"−7", Anchor{}, big.NewRat(-7, 1), "-7"},
		{"+.25", Anchor{}, big.NewRat(1, 4), "0.25"},
		{"1,234.5", Anchor{}, big.NewRat(2469, 2), "1234.5"},
		{"1.234,5", Anchor{}, big.NewRat(2469, 2), "1234.5"},
		{"1,234,567", Anchor{}, big.NewRat(1234567, 1), "1234567"},
		{"1 234,5", Anchor{}, big.NewRat(2469, 2), "1234.5"},
		{"1 234 567", Anchor{}, big.NewRat(1234567, 1), "1234567"},
		{"1'234.50", Anchor{}, big.NewRat(2469, 2), "1234.5"},
		{"1,5", Anchor{}, big.NewRat(3, 2), "1.5"},
		{"0.123", Anchor{}, big.NewRat(123, 1000), "0.123"},
		{"1,234", Anchor{Point: '.'}, big.NewRat(1234, 1), "1234"},
		{"1,234", Anchor{Point: ','}, big.NewRat(617, 500), "1.234"},
		{"1/3", Anchor{}, big.NewRat(1, 3), "1/3"},
		{"-3/4", Anchor{}, big.NewRat(-3, 4), "-0.75"},
		{"0.1", Anchor{}, big.NewRat(1, 10), "0.1"},
	} {
		got, err := Number.Parse(c.a, c.in)
		if err != nil {
			t.Errorf("Number(%q): %v", c.in, err)
			continue
		}
		if got.Cmp(c.want) != 0 {
			t.Errorf("Number(%q) = %v, want %v", c.in, got, c.want)
		}
		if s := Number.Canon(got); s != c.canon {
			t.Errorf("Canon(Number(%q)) = %q, want %q", c.in, s, c.canon)
		}
	}
}

func TestQuantity(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"5 kg", "5 kg"},
		{"5kg", "5 kg"},
		{"2.50 Kilograms", "2.5 kg"},
		{"12.5%", "12.5 %"},
		{"12.5 per cent", "12.5 %"},
		{"3 metres", "3 m"},
		{"72°F", "72 °F"},
		{"20 ℃", "20 °C"},
		{"1.5 l", "1.5 L"},
		{"6'", "6 '"},
		{"1 000 Mm", "1000 Mm"},
		{"2 tons", "2 tons"},
		{"3 gallons", "3 gallons"},
	} {
		if got, err := Quantity.Norm(Anchor{}, c.in); err != nil || got != c.want {
			t.Errorf("Quantity(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestMoney(t *testing.T) {
	for _, c := range []struct {
		in   string
		a    Anchor
		want string
	}{
		{"USD 5", Anchor{}, "5 USD"},
		{"5.00 EUR", Anchor{}, "5 EUR"},
		{"EUR1.500,00", Anchor{}, "1500 EUR"},
		{"EUR 1.500", Anchor{Point: ','}, "1500 EUR"},
		{"€5", Anchor{}, "5 EUR"},
		{"5 €", Anchor{}, "5 EUR"},
		{"US$1,250.75", Anchor{}, "1250.75 USD"},
		{"CA$ 20", Anchor{}, "20 CAD"},
		{"JP¥500", Anchor{}, "500 JPY"},
		{"CN¥500", Anchor{}, "500 CNY"},
		{"-€3.10", Anchor{}, "-3.1 EUR"},
		{"USD -3.10", Anchor{}, "-3.1 USD"},
		{"100 zł", Anchor{}, "100 PLN"},
		{"ALL 5", Anchor{}, "5 ALL"},
	} {
		if got, err := Money.Norm(c.a, c.in); err != nil || got != c.want {
			t.Errorf("Money(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// settles holds Canon∘Parse to being idempotent: a value's canonical text
// reads back to the same canonical text, so every spelling of a value settles
// on one string and settling it again changes nothing.
func settles[T any](t *testing.T, k Kind[T], a Anchor, in ...string) {
	t.Helper()
	for _, s := range in {
		once, err := k.Norm(a, s)
		if err != nil {
			t.Errorf("%v(%q): %v", k, s, err)
			continue
		}
		twice, err := k.Norm(Anchor{Point: '.'}, once)
		if err != nil || twice != once {
			t.Errorf("%v(%q) = %q, and again %q, %v; want it unchanged", k, s, once, twice, err)
		}
	}
}

func TestCanon(t *testing.T) {
	york, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	a := Anchor{At: day(2024, 3, 1), Zone: york, Order: DMY}
	settles(t, Date, a,
		"2023", "2023-03", "March 2023", "01/03/2023", "yesterday", "0001-01-01",
		"2023-03-01T10:00", "2023-03-01T10:00:00", "2023-03-01T10:00:00.000", "2023-03-01T10:00:00.123456789Z",
		"2023-03-01T10:00:00-02:30", "2023-03-01T00:00:00Z/2023-03-02T00:00:00.5+01:00")
	settles(t, Number, a,
		"0", "-0", "1.234,5", "1,234.5", "1 000 000", "0.001", "1/3", "-22/7", "1/8", "123456789012345678901234567890.5")
	settles(t, Quantity, a, "5 kg", "1,5 kilograms", "12.5%", "6'", "-40 degrees fahrenheit")
	settles(t, Money, a, "€5", "USD 1,234.50", "JP¥ 500", "-EUR 0.10", "2/3 USD")
}
