// Package jsoninput validates lossless JSON string encoding before decoding.
package jsoninput

import "strconv"

// ValidUnicodeEscapes rejects unpaired surrogates without altering literal replacement characters.
func ValidUnicodeEscapes(b []byte) bool {
	inString := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return false
		}
		if b[i] != 'u' {
			continue
		}
		if i+4 >= len(b) {
			return false
		}
		code, err := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xD800 && code <= 0xDBFF {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		} else if code >= 0xDC00 && code <= 0xDFFF {
			return false
		}
	}
	return true
}
