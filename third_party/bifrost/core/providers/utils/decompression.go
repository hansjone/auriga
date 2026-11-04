// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"compress/gzip"
	"compress/zlib"

	"io"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

var zstdDecoderPool = sync.Pool{
	New: func() any {
		dec, err := zstd.NewReader(nil, zstdDecoderOptions...)
		if err != nil {

			return nil
		}
		return dec
	},
}
var gzipReaderPool = sync.Pool{
	New: func() any {
		return &gzip.Reader{}
	},
}
var deflateReaderPool = sync.Pool{}

type deflateReader interface {
	io.ReadCloser
	Reset(r io.Reader, dict []byte) error
}

var zstdDecoderOptions = []zstd.DOption{
	zstd.WithDecoderConcurrency(1),
	zstd.WithDecoderMaxWindow(ZstdDecoderMaxWindow),
	zstd.WithDecoderMaxMemory(ZstdDecoderMaxMemory),
}
var brotliReaderPool = sync.Pool{
	New: func() any {
		return brotli.NewReader(nil)
	},
}

func safeReset(resetFn func() error) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	return resetFn() == nil
}

func AcquireGzipReader(r io.Reader) (*gzip.Reader, error) {
	if v := gzipReaderPool.Get(); v != nil {
		if gz, ok := v.(*gzip.Reader); ok {
			if safeReset(func() error { return gz.Reset(r) }) {
				return gz, nil
			}
		}

	}
	return gzip.NewReader(r)
}

func ReleaseGzipReader(gz *gzip.Reader) {
	if gz == nil {
		return
	}
	_ = gz.Close()
	gzipReaderPool.Put(gz)
}

func AcquireFlateReader(r io.Reader) (io.ReadCloser, error) {
	if v := deflateReaderPool.Get(); v != nil {
		if dr, ok := v.(deflateReader); ok {
			if safeReset(func() error { return dr.Reset(r, nil) }) {
				return dr, nil
			}
		}
	}
	return zlib.NewReader(r)
}

func ReleaseFlateReader(fr io.ReadCloser) {
	if fr == nil {
		return
	}
	_ = fr.Close()
	deflateReaderPool.Put(fr)
}

func AcquireBrotliReader(r io.Reader) *brotli.Reader {
	if v := brotliReaderPool.Get(); v != nil {
		if br, ok := v.(*brotli.Reader); ok {

			if safeReset(func() error { br.Reset(r); return nil }) {
				return br
			}
		}

	}
	return brotli.NewReader(r)
}

func ReleaseBrotliReader(br *brotli.Reader) {
	if br == nil {
		return
	}
	br.Reset(nil)
	brotliReaderPool.Put(br)
}

const (
	ZstdDecoderMaxWindow uint64 = 8 << 20
	ZstdDecoderMaxMemory uint64 = 100 << 20
)

func AcquireZstdDecoder(r io.Reader) (*zstd.Decoder, error) {
	if v := zstdDecoderPool.Get(); v != nil {
		if dec, ok := v.(*zstd.Decoder); ok && dec != nil {
			if safeReset(func() error { return dec.Reset(r) }) {
				return dec, nil
			}

			_ = dec.Reset(nil)
		}
	}
	dec, err := zstd.NewReader(r, zstdDecoderOptions...)
	if err != nil {
		if dec != nil {
			dec.Close()
		}
		return nil, err
	}
	return dec, nil
}

func ReleaseZstdDecoder(dec *zstd.Decoder) {
	if dec == nil {
		return
	}
	_ = dec.Reset(nil)
	zstdDecoderPool.Put(dec)
}
