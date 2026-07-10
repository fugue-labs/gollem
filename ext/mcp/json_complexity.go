package mcp

import (
	"errors"
	"io"
)

var (
	errHTTPJSONDepthLimit  = errors.New("mcp: HTTP JSON nesting depth exhausted")
	errHTTPJSONTokenLimit  = errors.New("mcp: HTTP JSON structural token capacity exhausted")
	errHTTPJSONInvalidUTF8 = errors.New("mcp: HTTP JSON contains invalid UTF-8")
)

// jsonComplexityReader bounds the object graph that encoding/json may build
// without allocating a parallel syntax tree. It lexes incrementally as the
// existing streaming decoder reads the request. Braces inside strings are
// ignored, escape state survives read boundaries, and encoding/json remains
// responsible for complete JSON syntax validation.
//
// A structural token is an object or array opener, an object-key string, or a
// scalar value. Counting keys as well as values tracks the dominant map-entry
// allocation that a byte-only request limit misses. Closing delimiters and
// punctuation do not allocate nodes and are not counted.
type jsonComplexityReader struct {
	reader io.Reader

	maxDepth  int
	maxTokens int

	depth       int
	tokens      int
	inString    bool
	escaped     bool
	inPrimitive bool

	// encoding/json replaces every invalid UTF-8 byte with the three-byte
	// encoding of U+FFFD. Without a preflight, a small-token byte string can
	// therefore expand threefold in every decoded copy and bypass the object-
	// graph envelope. Track UTF-8 incrementally so split runes are accepted but
	// malformed, overlong, surrogate, out-of-range, and truncated sequences are
	// rejected before the decoder sees an offending byte.
	utf8Remaining uint8
	utf8NextMin   byte
	utf8NextMax   byte
	terminalErr   error
}

func newJSONComplexityReader(reader io.Reader, maxDepth, maxTokens int) io.Reader {
	return &jsonComplexityReader{
		reader:    reader,
		maxDepth:  maxDepth,
		maxTokens: maxTokens,
	}
}

func (r *jsonComplexityReader) Read(p []byte) (int, error) {
	if r.terminalErr != nil {
		return 0, r.terminalErr
	}
	n, readErr := r.reader.Read(p)
	if n == 0 {
		if errors.Is(readErr, io.EOF) && r.utf8Remaining != 0 {
			r.terminalErr = errHTTPJSONInvalidUTF8
			return 0, r.terminalErr
		}
		return 0, readErr
	}
	if stop, utf8Err := r.validateUTF8(p[:n]); utf8Err != nil {
		r.terminalErr = utf8Err
		// Return only the valid prefix. If a multibyte sequence began in an
		// earlier read, stop is zero and encoding/json receives the error
		// before it can complete or replace the malformed rune.
		if stop > 0 {
			return stop, nil
		}
		return 0, utf8Err
	}
	if stop, scanErr := r.scan(p[:n]); scanErr != nil {
		r.terminalErr = scanErr
		// Do not expose the offending byte (or anything after it) to the JSON
		// decoder. Returning a clean prefix first makes Reader semantics valid;
		// the next read deterministically returns the limit error.
		if stop > 0 {
			return stop, nil
		}
		return 0, scanErr
	}
	if errors.Is(readErr, io.EOF) && r.utf8Remaining != 0 {
		// A Reader may legally return data and io.EOF together. Let the decoder
		// consume this otherwise-valid prefix, then surface the truncated rune
		// deterministically on its next read.
		r.terminalErr = errHTTPJSONInvalidUTF8
		return n, nil
	}
	return n, readErr
}

// validateUTF8 is an allocation-free incremental UTF-8 DFA. It returns the
// first invalid byte offset, or len(data) when every byte seen so far is a
// valid prefix. utf8Remaining may be non-zero at a read boundary; EOF handling
// in Read turns that incomplete prefix into errHTTPJSONInvalidUTF8.
func (r *jsonComplexityReader) validateUTF8(data []byte) (int, error) {
	for index, b := range data {
		if r.utf8Remaining != 0 {
			if b < r.utf8NextMin || b > r.utf8NextMax {
				return index, errHTTPJSONInvalidUTF8
			}
			r.utf8Remaining--
			if r.utf8Remaining != 0 {
				r.utf8NextMin = 0x80
				r.utf8NextMax = 0xbf
			}
			continue
		}

		switch {
		case b <= 0x7f:
			// ASCII, including JSON escapes and punctuation.
		case b >= 0xc2 && b <= 0xdf:
			r.startUTF8Sequence(1, 0x80, 0xbf)
		case b == 0xe0:
			r.startUTF8Sequence(2, 0xa0, 0xbf) // exclude overlong encodings
		case b >= 0xe1 && b <= 0xec:
			r.startUTF8Sequence(2, 0x80, 0xbf)
		case b == 0xed:
			r.startUTF8Sequence(2, 0x80, 0x9f) // exclude UTF-16 surrogates
		case b >= 0xee && b <= 0xef:
			r.startUTF8Sequence(2, 0x80, 0xbf)
		case b == 0xf0:
			r.startUTF8Sequence(3, 0x90, 0xbf) // exclude overlong encodings
		case b >= 0xf1 && b <= 0xf3:
			r.startUTF8Sequence(3, 0x80, 0xbf)
		case b == 0xf4:
			r.startUTF8Sequence(3, 0x80, 0x8f) // stop at U+10FFFF
		default:
			return index, errHTTPJSONInvalidUTF8
		}
	}
	return len(data), nil
}

func (r *jsonComplexityReader) startUTF8Sequence(remaining uint8, nextMin, nextMax byte) {
	r.utf8Remaining = remaining
	r.utf8NextMin = nextMin
	r.utf8NextMax = nextMax
}

func (r *jsonComplexityReader) scan(data []byte) (int, error) {
	for index, b := range data {
		if r.inString {
			switch {
			case r.escaped:
				r.escaped = false
			case b == '\\':
				r.escaped = true
			case b == '"':
				r.inString = false
			}
			continue
		}

		if r.inPrimitive {
			if !jsonPrimitiveDelimiter(b) {
				continue
			}
			r.inPrimitive = false
			// The delimiter still needs normal processing (notably ] and }).
		}

		switch b {
		case ' ', '\t', '\r', '\n', ',', ':':
			continue
		case '}', ']':
			if r.depth > 0 {
				r.depth--
			}
		case '{', '[':
			if err := r.addToken(); err != nil {
				return index, err
			}
			r.depth++
			if r.depth > r.maxDepth {
				return index, errHTTPJSONDepthLimit
			}
		case '"':
			if err := r.addToken(); err != nil {
				return index, err
			}
			r.inString = true
		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 't', 'f', 'n':
			if err := r.addToken(); err != nil {
				return index, err
			}
			r.inPrimitive = true
		default:
			// Invalid JSON is deliberately left to encoding/json. Every valid
			// scalar starts with one of the bytes handled above, so malformed
			// input cannot bypass the limit and later become an object graph.
		}
	}
	return len(data), nil
}

func (r *jsonComplexityReader) addToken() error {
	if r.tokens >= r.maxTokens {
		return errHTTPJSONTokenLimit
	}
	r.tokens++
	return nil
}

func jsonPrimitiveDelimiter(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', ',', '}', ']':
		return true
	default:
		return false
	}
}
