package address

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestKeccak256(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"},
		{"abc", "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45"},
		{"The quick brown fox jumps over the lazy dog", "4d741b6f1eb29cb2a9b9911c82f56fa8d73b04959d3d9d222895df6c0b28aa15"},
	} {
		got := keccak256([]byte(c.in))
		if hex.EncodeToString(got[:]) != c.want {
			t.Errorf("keccak256(%q) = %x, want %s", c.in, got, c.want)
		}
	}
}

func TestPayoutAddress(t *testing.T) {
	// The EIP-55 examples.
	checksummed := []string{
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
		"0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
	}
	for _, a := range checksummed {
		got, err := Parse(a)
		if err != nil || got != a {
			t.Errorf("Parse(%s) = %q, %v; want it accepted as is", a, got, err)
		}
		// All lower or all upper case carries no checksum and is accepted,
		// coming back in checksum form.
		for _, plain := range []string{"0x" + strings.ToLower(a[2:]), "0x" + strings.ToUpper(a[2:])} {
			got, err := Parse(plain)
			if err != nil || got != a {
				t.Errorf("Parse(%s) = %q, %v; want %s", plain, got, err, a)
			}
		}
		// Surrounding spaces from a paste are dropped.
		if got, err := Parse("  " + a + "\n"); err != nil || got != a {
			t.Errorf("Parse with spaces = %q, %v", got, err)
		}
	}

	// One letter's case flipped breaks the checksum.
	for _, bad := range []string{
		"0x5aaeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xFB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9adb",
	} {
		if _, err := Parse(bad); !errors.Is(err, ErrChecksum) {
			t.Errorf("Parse(%s) error %v, want the checksum error", bad, err)
		}
	}

	for _, bad := range []string{
		"",
		"0x",
		"5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0X5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED",
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAe",
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAedd",
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeg",
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeA d",
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1Be\"d",
		"0x+aAeb6053F3E94C9b9A09f33669435E7Ef1BeAe",
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAé",
	} {
		if _, err := Parse(bad); !errors.Is(err, ErrFormat) {
			t.Errorf("Parse(%q) error %v, want the format error", bad, err)
		}
	}
}
