package runtimetypes_test

import "time"

// tick lets the wall clock advance between two writes. Rows are ordered by
// created_at (then id), and some platforms (Windows) have a coarse clock, so
// tests that assert insertion order or "updated after created" must not rely
// on two back-to-back writes landing in different clock ticks.
func tick() { time.Sleep(20 * time.Millisecond) }
