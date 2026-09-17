package dedupe

import (
	"hash/fnv"
	"math"
	"math/bits"
)

// Hash is the SimHash of a text: 64 bits over its words, in which two texts
// sharing most of their words differ in few bit positions. Unlike a digest it
// is meant to be compared, not looked up — Near is the comparison.
//
// It costs one pass over the text and 8 bytes to keep, which is what makes it
// worth having: a million texts can be held in memory and compared without
// their words.
func Hash(s string) uint64 {
	var count [64]int
	for w := range words(fold(s)) {
		h := digest(w)
		for i := range 64 {
			if h&(1<<uint(i)) != 0 {
				count[i]++
			} else {
				count[i]--
			}
		}
	}
	var out uint64
	for i, c := range count {
		if c > 0 {
			out |= 1 << uint(i)
		}
	}
	return out
}

// Near scores two SimHashes on 0 to 1: one minus the share of the 64 bits
// they differ in. Identical texts score 1; unrelated texts score near 0.5,
// which is where half the bits differ by chance — Near is a screen, not a
// verdict, and the pairs it lets through are still compared properly.
func Near(a, b uint64) float64 {
	return 1 - float64(bits.OnesCount64(a^b))/64
}

// Sketch is a MinHash signature: for each of several independent hashes, the
// smallest value that hash gives any word of a text. Two sketches of the same
// width estimate how much their texts' word sets overlap, without either set
// being kept.
//
// The estimate is unbiased and its standard error is one over the square root
// of the width: 128 hashes put it within about nine points.
type Sketch []uint64

// Sign returns the MinHash signature of s over width hashes. A width of zero
// or less takes 64, which is enough to tell a near-duplicate from a
// coincidence.
func Sign(s string, width int) Sketch {
	if width <= 0 {
		width = 64
	}
	out := make(Sketch, width)
	for i := range out {
		out[i] = math.MaxUint64
	}
	for w := range words(fold(s)) {
		h := digest(w)
		for i := range out {
			if v := mix(h ^ seed(i)); v < out[i] {
				out[i] = v
			}
		}
	}
	return out
}

// Like estimates the Jaccard overlap of the two texts the sketches were taken
// from: the share of hash positions where they agree. Sketches of different
// widths cannot be compared and score 0. Two sketches of empty texts agree
// everywhere and score 1, as two empty word sets do.
func (s Sketch) Like(t Sketch) float64 {
	if len(s) == 0 || len(s) != len(t) {
		return 0
	}
	hit := 0
	for i := range s {
		if s[i] == t[i] {
			hit++
		}
	}
	return float64(hit) / float64(len(s))
}

// digest hashes one word. FNV-1a, so the same word hashes the same in every
// process and every run — a sketch kept on disk stays comparable.
func digest(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// seed is the ith hash's constant, taken from the golden ratio so that
// successive seeds share no low-order structure.
func seed(i int) uint64 { return mix(uint64(i+1) * 0x9e3779b97f4a7c15) }

// mix is the splitmix64 finalizer: it spreads a hash's bits so that the
// minimum over one seeded family says nothing about the minimum over another.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
