package mcp

import (
	"errors"
	"io"
)

var (
	errHTTPJSONDepthLimit = errors.New("mcp: HTTP JSON nesting depth exhausted")
	errHTTPJSONTokenLimit = errors.New("mcp: HTTP JSON structural token capacity exhausted")
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
	terminalErr error
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
		return 0, readErr
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
	return n, readErr
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
