package streamutil

import (
	"errors"
	"io"
	"iter"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// StreamTextOptions configures text streaming behavior.
type StreamTextOptions struct {
	Delta    bool          // if true, yield deltas; if false, yield accumulated text
	Debounce time.Duration // group events within this window (0 = no debounce)
}

// StreamText wraps a StreamedResponse to yield text according to options.
func StreamText(stream core.StreamedResponse, opts StreamTextOptions) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		var accumulated string
		var lastYield time.Time
		var pending string

		for {
			event, err := stream.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Yield any pending debounced content.
					if pending != "" {
						if opts.Delta {
							yield(pending, nil)
						} else {
							yield(accumulated, nil)
						}
					}
					return
				}
				yield("", err)
				return
			}

			delta, ok := event.(core.PartDeltaEvent)
			if !ok {
				continue
			}
			textDelta, ok := delta.Delta.(core.TextPartDelta)
			if !ok {
				continue
			}

			accumulated += textDelta.ContentDelta
			pending += textDelta.ContentDelta

			// Apply debounce.
			if opts.Debounce > 0 && time.Since(lastYield) < opts.Debounce {
				continue
			}

			var text string
			if opts.Delta {
				text = pending
			} else {
				text = accumulated
			}
			pending = ""
			lastYield = time.Now()

			if !yield(text, nil) {
				return
			}
		}
	}
}

// StreamTextDelta is a convenience for delta mode streaming.
func StreamTextDelta(stream core.StreamedResponse) iter.Seq2[string, error] {
	return StreamText(stream, StreamTextOptions{Delta: true})
}

// StreamTextAccumulated is a convenience for accumulated mode streaming.
func StreamTextAccumulated(stream core.StreamedResponse) iter.Seq2[string, error] {
	return StreamText(stream, StreamTextOptions{Delta: false})
}

// StreamTextDebounced wraps streaming with debounce grouping.
func StreamTextDebounced(stream core.StreamedResponse, debounce time.Duration) iter.Seq2[string, error] {
	return StreamText(stream, StreamTextOptions{Delta: true, Debounce: debounce})
}
