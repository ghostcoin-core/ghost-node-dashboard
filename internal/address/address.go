// Package address checks payout addresses: 0x and 40 hex characters, with
// the mixed-case checksum (EIP-55) checked when one is given.
package address

import (
	"encoding/hex"
	"errors"
	"strings"
)

// Errors Parse returns, in plain words.
var (
	ErrFormat   = errors.New("a payout address is 0x followed by 40 hex characters (0-9, a-f)")
	ErrChecksum = errors.New("the address has mixed upper and lower case letters that do not match its checksum; check it for typing mistakes, or enter it all in lower case")
)

// Parse checks s and returns it in checksum form. An address all in lower
// or all in upper case has no checksum to check; one in mixed case must
// match its checksum.
func Parse(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) != 42 || s[:2] != "0x" {
		return "", ErrFormat
	}
	digits := s[2:]
	if _, err := hex.DecodeString(digits); err != nil {
		return "", ErrFormat
	}
	sum := Checksum(digits)
	lower, upper := strings.ToLower(digits), strings.ToUpper(digits)
	if digits != lower && digits != upper && "0x"+digits != sum {
		return "", ErrChecksum
	}
	return sum, nil
}

// Checksum writes 40 hex digits in EIP-55 mixed case with the 0x prefix:
// a letter is upper case when the matching hex digit of the Keccak-256
// hash of the lower-case address is 8 or more.
func Checksum(digits string) string {
	lower := strings.ToLower(digits)
	hash := keccak256([]byte(lower))
	out := []byte(lower)
	for i, c := range out {
		if c < 'a' || c > 'f' {
			continue
		}
		nibble := hash[i/2]
		if i%2 == 0 {
			nibble >>= 4
		}
		if nibble&0x0f >= 8 {
			out[i] = c - 'a' + 'A'
		}
	}
	return "0x" + string(out)
}
