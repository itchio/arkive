// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zip

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"sync"

	"github.com/itchio/arkive/pflate"
	"github.com/itchio/kompress/flate"
)

// A Compressor returns a new compressing writer, writing to w.
// The WriteCloser's Close method must be used to flush pending data to w.
// The Compressor itself must be safe to invoke from multiple goroutines
// simultaneously, but each returned writer will be used only by
// one goroutine at a time.
type Compressor func(s CompressionSettings, w io.Writer) (io.WriteCloser, error)

// A Decompressor returns a new decompressing reader, reading from r.
// The ReadCloser's Close method must be used to release associated resources.
// The Decompressor itself must be safe to invoke from multiple goroutines
// simultaneously, but each returned reader will be used only by
// one goroutine at a time.
type Decompressor func(r io.Reader, f *File) io.ReadCloser

type CompressionSettings struct {
	Flate FlateSettings
}

type FlateSettings struct {
	// must be between flate.NoCompression and flate.BestCompression (inclusive)
	Level int
	// Defaults to 256KiB (Minimum 16KiB)
	BlockSize int
	// Defaults to 16 (Minimum 1)
	Blocks int
}

func (fs *FlateSettings) Validate() error {
	if fs.Level < -2 || fs.Level > 9 {
		return fmt.Errorf("flate settings: level must be within [-2,9], was %d", fs.Level)
	}
	if fs.Blocks <= 0 {
		return fmt.Errorf("flate settings: blocks must be equal or greater than 0")
	}
	if fs.BlockSize <= 16384 {
		return fmt.Errorf("flate settings: block size must be equal or greater than 16384")
	}
	return nil
}

func (cs *CompressionSettings) Validate() error {
	err := cs.Flate.Validate()
	if err != nil {
		return err
	}

	return nil
}

var defaultCompressionSettings = CompressionSettings{
	Flate: FlateSettings{
		Level:     flate.DefaultCompression,
		BlockSize: 256 * 1024, // 256KiB
		Blocks:    16,
	},
}

var bestCompressionSettings = CompressionSettings{
	Flate: FlateSettings{
		Level:     flate.BestCompression,
		BlockSize: 16 * 1024 * 1024, // 16MiB
		Blocks:    16,
	},
}

func DefaultCompressionSettings() CompressionSettings {
	return defaultCompressionSettings
}

func BestCompressionSettings() CompressionSettings {
	return bestCompressionSettings
}

func newFlateWriter(s CompressionSettings, w io.Writer) io.WriteCloser {
	fw, _ := pflate.NewWriter(w, s.Flate.Level)
	// error ignored on purpose
	_ = fw.SetConcurrency(s.Flate.BlockSize, s.Flate.Blocks)
	return fw
}

var flateReaderPool sync.Pool

func newFlateReader(r io.Reader, f *File) io.ReadCloser {
	fr, ok := flateReaderPool.Get().(io.ReadCloser)
	if ok {
		// ignoring error on purpose
		_ = fr.(flate.Resetter).Reset(r, nil)
	} else {
		fr = flate.NewReader(r)
	}
	return &pooledFlateReader{fr: fr}
}

type pooledFlateReader struct {
	mu sync.Mutex // guards Close and Read
	fr io.ReadCloser
}

func (r *pooledFlateReader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fr == nil {
		return 0, errors.New("Read after Close")
	}
	return r.fr.Read(p)
}

func (r *pooledFlateReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var err error
	if r.fr != nil {
		err = r.fr.Close()
		flateReaderPool.Put(r.fr)
		r.fr = nil
	}
	return err
}

var (
	compressors   sync.Map // map[uint16]Compressor
	decompressors sync.Map // map[uint16]Decompressor
)

func init() {
	compressors.Store(Store, Compressor(func(s CompressionSettings, w io.Writer) (io.WriteCloser, error) { return &nopCloser{w}, nil }))
	compressors.Store(Deflate, Compressor(func(s CompressionSettings, w io.Writer) (io.WriteCloser, error) { return newFlateWriter(s, w), nil }))

	decompressors.Store(Store, Decompressor(func(r io.Reader, f *File) io.ReadCloser { return ioutil.NopCloser(r) }))
	decompressors.Store(Deflate, Decompressor(newFlateReader))
}

// RegisterDecompressor allows custom decompressors for a specified method ID.
// The common methods Store and Deflate are built in.
func RegisterDecompressor(method uint16, dcomp Decompressor) {
	if _, dup := decompressors.LoadOrStore(method, dcomp); dup {
		panic("decompressor already registered")
	}
}

// RegisterCompressor registers custom compressors for a specified method ID.
// The common methods Store and Deflate are built in.
func RegisterCompressor(method uint16, comp Compressor) {
	if _, dup := compressors.LoadOrStore(method, comp); dup {
		panic("compressor already registered")
	}
}

func compressor(method uint16) Compressor {
	ci, ok := compressors.Load(method)
	if !ok {
		return nil
	}
	return ci.(Compressor)
}

func decompressor(method uint16) Decompressor {
	di, ok := decompressors.Load(method)
	if !ok {
		return nil
	}
	return di.(Decompressor)
}
