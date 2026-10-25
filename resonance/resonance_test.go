package resonance

import (
    "errors"
    "math"
    "testing"
)

func closeTo(t *testing.T, got, want float64) {
    t.Helper()
    if math.Abs(got-want) > 1e-6 { t.Fatalf("got %.9f, want %.9f", got, want) }
}

func mustEmbed(t *testing.T, v []float32) Wave {
    t.Helper()
    w, err := Embed(v)
    if err != nil { t.Fatal(err) }
    return w
}

func mustScore(t *testing.T, a, b Wave) float64 {
    t.Helper()
    s, err := Score(a, b)
    if err != nil { t.Fatal(err) }
    return s
}

func TestRealCosineEquivalence(t *testing.T) {
    x := mustEmbed(t, []float32{1,0})
    closeTo(t, mustScore(t,x,x),1)
    closeTo(t, mustScore(t,x,mustEmbed(t,[]float32{0,1})),0.5)
    closeTo(t, mustScore(t,x,mustEmbed(t,[]float32{-1,0})),0)
}

func TestExplicitNegationIsNotInferred(t *testing.T) {
    x := mustEmbed(t, []float32{0.8,0.6})
    closeTo(t,mustScore(t,x,x.Negate()),0)
    closeTo(t,mustScore(t,x.Negate(),x.Negate()),1)
    closeTo(t,mustScore(t,x,mustEmbed(t,[]float32{0.8,0.6})),1)
}

func TestEnergyImbalance(t *testing.T) {
    closeTo(t,mustScore(t,mustEmbed(t,[]float32{1}),mustEmbed(t,[]float32{2})),0.72)
}

func TestRotationInvariance(t *testing.T) {
    a,err:=Polar([]float32{1,2},[]float32{0.4,-1})
    if err!=nil {t.Fatal(err)}
    b,err:=Polar([]float32{2,1},[]float32{-0.5,0.7})
    if err!=nil {t.Fatal(err)}
    ar,err:=a.Rotate(0.8)
    if err!=nil {t.Fatal(err)}
    br,err:=b.Rotate(0.8)
    if err!=nil {t.Fatal(err)}
    closeTo(t,mustScore(t,a,b),mustScore(t,ar,br))
}

func TestValidation(t *testing.T) {
    if _,err:=Embed(nil);!errors.Is(err,ErrDim){t.Fatal(err)}
    if _,err:=Embed([]float32{float32(math.NaN())});!errors.Is(err,ErrFinite){t.Fatal(err)}
    if _,err:=Polar([]float32{-1},[]float32{0});!errors.Is(err,ErrAmplitude){t.Fatal(err)}
    if _,err:=Polar([]float32{1},nil);!errors.Is(err,ErrDim){t.Fatal(err)}
    if _,err:=Score(mustEmbed(t,[]float32{1}),mustEmbed(t,[]float32{1,0}));!errors.Is(err,ErrDim){t.Fatal(err)}
}

func TestRankOnScopedShortlist(t *testing.T) {
    pos := mustEmbed(t,[]float32{1,0})
    neg := pos.Negate()
    hits,err := Rank(neg, []Candidate{
        {ID:"asserted",Wave:pos,Cosine:1},
        {ID:"denied",Wave:neg,Cosine:1},
    },0.75,2)
    if err!=nil {t.Fatal(err)}
    if len(hits)!=2 || hits[0].ID!="denied" || hits[1].ID!="asserted" {t.Fatalf("bad ranking: %+v",hits)}
    closeTo(t,hits[0].Score,1)
    closeTo(t,hits[1].Score,0.25)
    if _,err:=Rank(neg,nil,1.5,1);!errors.Is(err,ErrWeight){t.Fatal(err)}
    if _,err:=Rank(neg,[]Candidate{{ID:"nan",Wave:neg,Cosine:math.NaN()}},0.5,1);!errors.Is(err,ErrCosine){t.Fatal(err)}
}

func TestZeroEnergy(t *testing.T) {
    z:=mustEmbed(t,[]float32{0,0})
    x:=mustEmbed(t,[]float32{1,0})
    closeTo(t,mustScore(t,z,x),0)
    closeTo(t,mustScore(t,z,z),0)
}
