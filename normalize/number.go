package normalize

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"unicode/utf8"
)

// Number reads a number exactly, as a rational: "1,234.5" is 2469/2, with no
// float anywhere to round it. It reads a sign — "-", "+" or the minus sign —
// digits, one decimal mark and groups of three separated by the other mark, a
// space, a narrow or non-breaking space, or an apostrophe; and "p/q". Its
// canonical form has a point, no grouping and no trailing zeros, and a value
// with no finite decimal — which only "p/q" can give — is written "p/q".
//
// Which mark is the decimal is the Anchor's to say. Without one, a mark is
// read where it can be one thing only: both marks present, the last is the
// decimal; one mark repeated, it groups; one mark once, with three digits
// after it and one to three before — "1,234", "1.234" — it could be either,
// and is refused with ErrAmbiguous.
var Number = Kind[*big.Rat]{Name: "number", Parse: number, Canon: decimal}

// Measure is a quantity: an exact number of a unit.
type Measure struct {
	Value *big.Rat
	Unit  string
}

// Quantity reads a number followed by its unit — "5 kg", "12.5%", "3 metres",
// "72°F" — reading the number as Number does. A unit written as a word is
// folded to its symbol where the word has one meaning: "kilograms" is kg,
// "litre" is L, "percent" is %. One that does not — "ton", "gallon", "pound",
// "ounce", each of which is two or three different units — is kept as written,
// as is any unit the table does not know. Nothing is converted.
var Quantity = Kind[Measure]{
	Name:  "quantity",
	Parse: measure,
	Canon: func(m Measure) string { return decimal(m.Value) + " " + m.Unit },
}

// Amount is a sum of money: an exact value in a currency, named by its ISO
// 4217 code.
type Amount struct {
	Value *big.Rat
	Code  string
}

// Money reads a sum of money: a number, as Number reads it, with an ISO 4217
// code before or after it — "USD 5", "5.00 EUR" — or with a sign only one
// currency writes — "€5", "5 zł", "US$5", "CA$5".
//
// A sign several currencies write — "$", "£", "¥", "₩", "kr", "C$" — is
// refused with ErrAmbiguous: "¥" is the yen and the yuan, and "$" a dozen
// currencies. A code must be uppercase and on ISO 4217's list, current or
// withdrawn since 1999, so "ALL 5" is lek and "all 5" is not money.
var Money = Kind[Amount]{
	Name:  "money",
	Parse: money,
	Canon: func(m Amount) string { return decimal(m.Value) + " " + m.Code },
}

// number reads a number under a.
func number(a Anchor, s string) (*big.Rat, error) {
	t := strings.TrimSpace(s)
	neg, t := sign(t)
	if p, q, ok := strings.Cut(t, "/"); ok {
		r, good := new(big.Rat).SetString(p + "/" + q)
		if !good || !figures(p) || !figures(q) {
			return nil, refuse("number", s, ErrSyntax, "a fraction is digits over digits")
		}
		if neg {
			r.Neg(r)
		}
		return r, nil
	}
	whole, frac, err := point(a, s, t)
	if err != nil {
		return nil, err
	}
	lit := whole
	if frac != "" {
		lit += "." + frac
	}
	r, ok := new(big.Rat).SetString(lit)
	if !ok {
		return nil, refuse("number", s, ErrSyntax, "not a number")
	}
	if neg {
		r.Neg(r)
	}
	return r, nil
}

// sign takes a leading sign off t.
func sign(t string) (bool, string) {
	for _, m := range []string{"-", "−", "+"} {
		if rest, ok := strings.CutPrefix(t, m); ok {
			return m != "+", rest
		}
	}
	return false, t
}

// figures reports a non-empty run of ASCII digits.
func figures(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// gap reports a rune that separates digit groups other than the two marks:
// the spaces and the apostrophes.
func gap(r rune) rune {
	switch r {
	case ' ', ' ', ' ', ' ':
		return ' '
	case '\'', '’':
		return '\''
	}
	return 0
}

// point splits the digits of t at its decimal mark, checking and removing the
// grouping, and returns the whole and fractional digits.
func point(a Anchor, s, t string) (whole, frac string, err error) {
	mark := a.Point
	switch mark {
	case 0:
		if mark, err = guess(s, t); err != nil {
			return "", "", err
		}
	case '.', ',':
	default:
		return "", "", fmt.Errorf("normalize: number %q: Anchor.Point %q is not '.' or ','", s, a.Point)
	}
	if mark != 0 {
		if i := strings.IndexRune(t, mark); i >= 0 {
			t, frac = t[:i], t[i+1:]
			if !figures(frac) {
				return "", "", refuse("number", s, ErrSyntax, "one decimal mark, and digits after it")
			}
		}
	}
	whole, err = groups(s, t, mark)
	if err != nil {
		return "", "", err
	}
	if whole == "" && frac == "" {
		return "", "", refuse("number", s, ErrSyntax, "no digits")
	}
	if whole == "" {
		whole = "0"
	}
	return whole, frac, nil
}

// guess finds the decimal mark of t with no Anchor to say, where the text
// allows only one reading, and refuses where it allows two. Zero means t has
// none.
func guess(s, t string) (rune, error) {
	dot, comma := strings.LastIndexByte(t, '.'), strings.LastIndexByte(t, ',')
	switch {
	case dot >= 0 && comma >= 0:
		if dot > comma {
			return '.', nil
		}
		return ',', nil
	case dot < 0 && comma < 0:
		return 0, nil
	}
	mark, at := rune('.'), dot
	if comma >= 0 {
		mark, at = ',', comma
	}
	if strings.Count(t, string(mark)) > 1 {
		return 0, nil // a mark that repeats groups
	}
	before, after := t[:at], t[at+1:]
	if strings.IndexFunc(before, func(r rune) bool { return gap(r) != 0 }) >= 0 {
		return mark, nil // grouped by spaces already, so the mark is the decimal
	}
	if len(after) == 3 && figures(after) && figures(before) && len(before) <= 3 && before[0] != '0' {
		return 0, refuse("number", s, ErrAmbiguous,
			fmt.Sprintf("%q may be a decimal mark or a thousands separator; Anchor.Point says which", mark))
	}
	return mark, nil
}

// groups checks the grouping of the whole part of a number and returns its
// digits. The groups are one separator throughout, the first of one to three
// digits and every other of three.
func groups(s, t string, mark rune) (string, error) {
	var (
		sep   rune
		parts []string
		cur   strings.Builder
	)
	for _, r := range t {
		switch {
		case r >= '0' && r <= '9':
			cur.WriteRune(r)
			continue
		case r == mark:
			return "", refuse("number", s, ErrSyntax, "more than one decimal mark")
		}
		g := gap(r)
		if r == '.' || r == ',' {
			g = r
		}
		if g == 0 {
			return "", refuse("number", s, ErrSyntax, fmt.Sprintf("%q is not part of a number", r))
		}
		if sep != 0 && g != sep {
			return "", refuse("number", s, ErrSyntax, "digits grouped two ways")
		}
		sep = g
		parts = append(parts, cur.String())
		cur.Reset()
	}
	parts = append(parts, cur.String())
	if sep == 0 {
		return parts[0], nil
	}
	for i, p := range parts {
		if (i == 0 && (len(p) < 1 || len(p) > 3)) || (i > 0 && len(p) != 3) {
			return "", refuse("number", s, ErrSyntax, "digits grouped in other than threes")
		}
	}
	return strings.Join(parts, ""), nil
}

// decimal writes a number in its canonical form: exact, with a point, no
// grouping and no trailing zeros; "p/q" when it has no finite decimal.
func decimal(r *big.Rat) string {
	if r == nil {
		return ""
	}
	if r.IsInt() {
		return r.Num().String()
	}
	// A reduced fraction has a finite decimal exactly when its denominator
	// is 2^a·5^b, and then max(a, b) digits of it are all of them.
	d := new(big.Int).Set(r.Denom())
	twos := d.TrailingZeroBits()
	d.Rsh(d, twos)
	var fives uint
	five, rem := big.NewInt(5), new(big.Int)
	for {
		q, m := new(big.Int).QuoRem(d, five, rem)
		if m.Sign() != 0 {
			break
		}
		d, fives = q, fives+1
	}
	if d.Cmp(big.NewInt(1)) != 0 {
		return r.RatString()
	}
	return r.FloatString(int(max(twos, fives)))
}

// measure reads a quantity under a.
func measure(a Anchor, s string) (Measure, error) {
	t := strings.TrimSpace(s)
	n, unit := lead(t)
	if unit == "" {
		return Measure{}, refuse("quantity", s, ErrSyntax, "no unit")
	}
	v, err := number(a, n)
	if err != nil {
		return Measure{}, fmt.Errorf("normalize: quantity %q: %w", s, err)
	}
	return Measure{Value: v, Unit: unitOf(unit)}, nil
}

// lead splits t into the number at its front and what follows. Spaces and
// apostrophes at the end of the number belong to what follows: "5 kg" and
// "6'" are both a number and a unit.
func lead(t string) (string, string) {
	_, rest := sign(t)
	i := len(t) - len(rest)
	for i < len(t) {
		r, w := utf8.DecodeRuneInString(t[i:])
		if !(r >= '0' && r <= '9' || r == '.' || r == ',' || gap(r) != 0) {
			break
		}
		i += w
	}
	n := strings.TrimRightFunc(t[:i], func(r rune) bool { return gap(r) != 0 })
	return n, strings.Join(strings.Fields(t[len(n):]), " ")
}

// units folds a unit written as a word, or as a variant of its symbol, onto
// the symbol. Only spellings with one meaning are here: a "ton" is short,
// long or metric, a "pound" is a mass or a currency, a "gallon" is American or
// imperial, and each of those is kept as written.
var units = map[string]string{
	"metre": "m", "metres": "m", "meter": "m", "meters": "m",
	"kilometre": "km", "kilometres": "km", "kilometer": "km", "kilometers": "km",
	"centimetre": "cm", "centimetres": "cm", "centimeter": "cm", "centimeters": "cm",
	"millimetre": "mm", "millimetres": "mm", "millimeter": "mm", "millimeters": "mm",
	"kilogram": "kg", "kilograms": "kg", "kilogramme": "kg", "kilogrammes": "kg",
	"gram": "g", "grams": "g", "gramme": "g", "grammes": "g",
	"milligram": "mg", "milligrams": "mg",
	"tonne": "t", "tonnes": "t",
	"litre": "L", "litres": "L", "liter": "L", "liters": "L",
	"millilitre": "mL", "millilitres": "mL", "milliliter": "mL", "milliliters": "mL",
	"second": "s", "seconds": "s", "sec": "s", "secs": "s",
	"millisecond": "ms", "milliseconds": "ms",
	"minute": "min", "minutes": "min", "mins": "min",
	"hour": "h", "hours": "h", "hr": "h", "hrs": "h",
	"day": "d", "days": "d",
	"mile": "mi", "miles": "mi", "foot": "ft", "feet": "ft",
	"inch": "in", "inches": "in", "yard": "yd", "yards": "yd",
	"percent": "%", "per cent": "%",
	"celsius": "°C", "degree celsius": "°C", "degrees celsius": "°C",
	"fahrenheit": "°F", "degree fahrenheit": "°F", "degrees fahrenheit": "°F",
	"kelvin": "K", "kelvins": "K",
}

// marks are symbol variants, matched exactly because a symbol's case is part
// of it: mm is a millimetre and Mm a megametre.
var marks = map[string]string{"l": "L", "ml": "mL", "℃": "°C", "℉": "°F", "° C": "°C", "° F": "°F"}

// unitOf is a unit's canonical spelling.
func unitOf(u string) string {
	if m, ok := marks[u]; ok {
		return m
	}
	if m, ok := units[strings.ToLower(u)]; ok {
		return m
	}
	return u
}

// money reads a sum of money under a.
func money(a Anchor, s string) (Amount, error) {
	t := strings.TrimSpace(s)
	neg, t := sign(t)
	code, rest, err := currency(s, t)
	if err != nil {
		return Amount{}, err
	}
	if _, unsigned := sign(rest); neg && unsigned != rest {
		return Amount{}, refuse("money", s, ErrSyntax, "two signs")
	}
	v, err := number(a, rest)
	if err != nil {
		return Amount{}, fmt.Errorf("normalize: money %q: %w", s, err)
	}
	if neg {
		v.Neg(v)
	}
	return Amount{Value: v, Code: code}, nil
}

// currency finds the currency at either end of t and returns its code and the
// rest.
func currency(s, t string) (string, string, error) {
	if len(t) >= 3 {
		if c := t[:3]; iso4217[c] {
			return c, strings.TrimSpace(t[3:]), nil
		}
		if c := t[len(t)-3:]; iso4217[c] {
			return c, strings.TrimSpace(t[:len(t)-3]), nil
		}
	}
	for _, sym := range signs {
		rest, ok := strings.CutPrefix(t, sym)
		if !ok {
			rest, ok = strings.CutSuffix(t, sym)
		}
		if !ok {
			continue
		}
		if code := symbols[sym]; code != "" {
			return code, strings.TrimSpace(rest), nil
		}
		return "", "", refuse("money", s, ErrAmbiguous,
			fmt.Sprintf("%q is written by %s; name the currency by its code", sym, shared[sym]))
	}
	return "", "", refuse("money", s, ErrSyntax, "no currency: an ISO 4217 code or a sign one currency owns")
}

// symbols are the currency signs and prefixed dollars that belong to one
// currency.
var symbols = map[string]string{
	"US$": "USD", "CA$": "CAD", "A$": "AUD", "AU$": "AUD", "NZ$": "NZD",
	"HK$": "HKD", "S$": "SGD", "R$": "BRL", "MX$": "MXN", "NT$": "TWD",
	"CN¥": "CNY", "JP¥": "JPY", "E£": "EGP",
	"€": "EUR", "₹": "INR", "₽": "RUB", "₺": "TRY", "₪": "ILS", "₫": "VND",
	"₴": "UAH", "₦": "NGN", "฿": "THB", "₱": "PHP", "₸": "KZT", "₾": "GEL",
	"₼": "AZN", "₲": "PYG", "₵": "GHS", "zł": "PLN", "Kč": "CZK", "Ft": "HUF",
}

// shared are the signs several currencies write, and who writes them.
var shared = map[string]string{
	"$":  "the US, Canadian, Australian, Mexican and a dozen other dollars and pesos",
	"£":  "the pound sterling and the Egyptian, Lebanese, Syrian and South Sudanese pounds",
	"¥":  "the Japanese yen and the Chinese yuan",
	"￥":  "the Japanese yen and the Chinese yuan",
	"₩":  "the South and North Korean won",
	"kr": "the Swedish, Norwegian, Danish and Icelandic krónur",
	"C$": "the Canadian dollar and the Nicaraguan córdoba",
	"₨":  "the Pakistani, Nepalese, Sri Lankan, Mauritian and Seychellois rupees",
	"₡":  "the Costa Rican and Salvadoran colones",
}

// signs is every sign, longest first, so "US$" is tried before "$".
var signs = func() []string {
	var out []string
	for s := range symbols {
		out = append(out, s)
	}
	for s := range shared {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return out
}()

// iso4217 are the currency codes: ISO 4217's current list, funds and metals
// included, and the codes withdrawn since 1999 that older documents still
// name.
var iso4217 = func() map[string]bool {
	m := map[string]bool{}
	for c := range strings.FieldsSeq(`
		AED AFN ALL AMD AOA ARS AUD AWG AZN BAM BBD BDT BHD BIF BMD BND BOB BOV
		BRL BSD BTN BWP BYN BZD CAD CDF CHE CHF CHW CLF CLP CNY COP COU CRC CUP
		CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF
		GTQ GYD HKD HNL HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR
		KMF KPW KRW KWD KYD KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT
		MOP MRU MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB PEN
		PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE
		SOS SRD SSP STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD TWD TZS UAH UGX
		USD USN UYI UYU UYW UZS VED VES VND VUV WST XAF XAG XAU XBA XBB XBC XBD
		XCD XCG XDR XOF XPD XPF XPT XSU XTS XUA XXX YER ZAR ZMW ZWG

		ADP AFA ANG ATS AZM BEF BGN BYB BYR CSD CUC CYP DEM EEK ESP FIM FRF GHC
		GRD HRK IEP ITL LTL LUF LVL MGF MRO MTL MZM NLG PTE ROL SDD SIT SKK SLL
		SRG STD TMM TRL VEB VEF XEU YUM ZMK ZWD ZWL ZWN ZWR`) {
		m[c] = true
	}
	return m
}()
