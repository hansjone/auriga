package main

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func invalidInstanceMemoryInputs() []string {
	return []string{"", "0", "00", "01", "0512", "-1", "+512", " 512", "512 ", "512\n", "512\t", "5 12", "512.0", "1e3", "0x200", "５１２", "٥١٢", "abc", "8796093022208", "9223372036854775807", "9223372036854775808", strings.Repeat("9", 100)}
}

func TestInstanceMaxMemoryMB(t *testing.T) {
	for _, memory := range []int64{1, 512, 1024, math.MaxInt64 / (1024 * 1024)} {
		value := strconv.FormatInt(memory, 10)
		t.Run("accept_"+value, func(t *testing.T) {
			got, err := parseInstanceMaxMemoryMB(value)
			if err != nil || got != memory {
				t.Fatalf("parse %q = %d, %v; want %d", value, got, err, memory)
			}
		})
	}
	for i, value := range invalidInstanceMemoryInputs() {
		t.Run("reject_"+strconv.Itoa(i), func(t *testing.T) {
			if got, err := parseInstanceMaxMemoryMB(value); err == nil || got != 0 {
				t.Fatalf("invalid memory %q = %d, %v", value, got, err)
			}
		})
	}
}
