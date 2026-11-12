// SPDX-License-Identifier: AGPL-3.0-or-later
// XC_VM_Fanout — https://github.com/Vateron-Media/XC_VM_Fanout
// See LICENSE and LICENSE-ADDITIONAL-TERMS.md

package tsjoin

import (
	"bytes"
	"testing"
)

// feedSizedGOP publishes one GOP of n filler packets after its keyframe.
func feedSizedGOP(s *State, pcr int64, n int) {
	s.Update(keyPCR(pcr))
	filler := pkt(map[int]byte{1: 0x01, 2: 0x01, 3: 0x10})
	s.Update(bytes.Repeat(filler, n))
}

// TestTheRingHoldsLittleMoreThanItsBytes: a block's array was grown by append
// and recycled with its capacity kept, so the arrays crept up to the largest
// GOPs the stream had shown: on a live node the rings held 1.6x their bytes
// (651 MB of stream in 1057 MB of arrays, plus 81 MB recycled), and a stream
// with a scene change every few GOPs reached 3x. Sized from the stream's
// recent GOPs and fitted when they close, the ring's arrays — free list
// included — hold little more than its bytes.
func TestTheRingHoldsLittleMoreThanItsBytes(t *testing.T) {
	const tick = 90000
	// GOP sizes that wander ±25% around 1500 packets (~280 KB), deterministic.
	sizes := []int{1500, 1700, 1300, 1600, 1450, 1800, 1200, 1550, 1650, 1400}
	for _, spiky := range []bool{false, true} {
		s := New(16<<20, 60_000) // a 60 s ring, GOPs 2 s apart
		for g := 0; g < 600; g++ {
			n := sizes[g%len(sizes)]
			if spiky && g%7 == 0 {
				n *= 3 // a scene change
			}
			feedSizedGOP(s, int64(g)*2*tick, n)
		}
		used, _, gops := s.RingStats()
		capBytes, freeBytes := s.RingCapacity()
		if gops < 20 {
			t.Fatalf("ring holds %d GOPs, want a full 60 s ring", gops)
		}
		if ratio := float64(capBytes+freeBytes) / float64(used); ratio > 1.3 {
			t.Errorf("spiky=%v: the ring's arrays hold %.2fx its bytes (%d + %d free over %d), want at most 1.3x", spiky, ratio, capBytes, freeBytes, used)
		}
	}
}

// TestAFittedBlockReadsTheSameUnderItsReader: fitting a closing block moves its
// bytes to a new array; a reader that already holds slices of the old one, and
// one that reads it afterwards, both see the same bytes at the same offsets.
func TestAFittedBlockReadsTheSameUnderItsReader(t *testing.T) {
	const tick = 90000
	s := New(16<<20, 60_000)
	for g := 0; g < 6; g++ {
		feedSizedGOP(s, int64(g)*2*tick, 1500)
	}
	feedGOP(s, 6*2*tick, 0x5A) // a smaller GOP: its array is well over when it closes
	_, c := s.JoinStart(nil, 0)
	before, _, _, _, pin := s.ReadFrom(c, 1<<30)
	held := bytes.Join(before, nil)
	feedSizedGOP(s, 7*2*tick, 1500) // closes the small block: it is fitted
	small := s.gops[len(s.gops)-2]
	if cap(small.data)-len(small.data) > len(small.data)/4 {
		t.Fatalf("the closed block keeps %d spare bytes over %d", cap(small.data)-len(small.data), len(small.data))
	}
	if !bytes.Equal(bytes.Join(before, nil), held) {
		t.Fatal("slices a reader held changed when the block was fitted")
	}
	s.Unpin(pin)
	_, c2 := s.JoinStart(nil, 60_000) // from the oldest block: through the fitted one
	after, _, _, _, pin2 := s.ReadFrom(c2, 1<<30)
	defer s.Unpin(pin2)
	if !bytes.Contains(bytes.Join(after, nil), small.data) {
		t.Fatal("a later read does not see the fitted block's bytes")
	}
}

// TestASizedBlockStillTakesABiggerGOP: a GOP larger than the estimate still
// goes in whole (its array grows as before), and the estimate moves toward it.
func TestASizedBlockStillTakesABiggerGOP(t *testing.T) {
	const tick = 90000
	s := New(16<<20, 60_000)
	for g := 0; g < 5; g++ {
		feedSizedGOP(s, int64(g)*2*tick, 100)
	}
	feedSizedGOP(s, 5*2*tick, 900)
	feedSizedGOP(s, 6*2*tick, 100)
	big := s.gops[len(s.gops)-2]
	if len(big.data) != 901*PacketSize {
		t.Fatalf("the big GOP holds %d bytes, want %d", len(big.data), 901*PacketSize)
	}
	if s.gopEst <= 101*PacketSize {
		t.Fatalf("estimate %d did not move toward the bigger GOP (%d)", s.gopEst, 901*PacketSize)
	}
}

// TestANewSourceForgetsTheOldSize: after a source change the next blocks are
// sized from the new source, not the old one.
func TestANewSourceForgetsTheOldSize(t *testing.T) {
	const tick = 90000
	s := New(16<<20, 60_000)
	for g := 0; g < 5; g++ {
		feedSizedGOP(s, int64(g)*2*tick, 2000)
	}
	s.Reset()
	if s.gopEst != 0 {
		t.Fatalf("estimate %d kept across a source change", s.gopEst)
	}
}
