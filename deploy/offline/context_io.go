package main

import (
	"context"
	"io"
)

// Retain cancellation between archive entries and bounded file-copy reads.
// Wrapping the reader also avoids io.Copy bypassing Read through WriterTo.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func cancellationReader(ctx context.Context, reader io.Reader) io.Reader {
	if ctx.Done() == nil {
		return reader
	}
	return contextReader{ctx: ctx, reader: reader}
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}
