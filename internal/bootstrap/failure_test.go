package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

type interruptedSource struct {
	delegate Source
	filename string
}

func (source *interruptedSource) Fetch(
	ctx context.Context,
	tag, filename string,
	maxBytes int64,
) ([]byte, error) {
	content, err := source.delegate.Fetch(ctx, tag, filename, maxBytes)
	if err != nil || filename != source.filename {
		return content, err
	}
	return bytes.Clone(content[:len(content)/2]), io.ErrUnexpectedEOF
}

type interruptedReadCloser struct {
	content []byte
	read    bool
}

func (reader *interruptedReadCloser) Read(target []byte) (int, error) {
	if reader.read {
		return 0, io.ErrUnexpectedEOF
	}
	reader.read = true
	count := copy(target, reader.content)
	return count, nil
}

func (*interruptedReadCloser) Close() error { return nil }

func TestTamperAndInterruptedDownloadsPreserveLastKnownGood(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*memorySource, string, string) Source
		wantedError error
	}{
		{
			name: "descriptor bytes tampered",
			mutate: func(source *memorySource, tag, _ string) Source {
				path := tag + "/" + descriptorFilename
				source.files[path] = bytes.Replace(source.files[path], []byte("2026-07-20"), []byte("2027-07-20"), 1)
				return source
			},
			wantedError: releasecontract.ErrSignatureInvalid,
		},
		{
			name: "detached signature tampered",
			mutate: func(source *memorySource, tag, _ string) Source {
				path := tag + "/" + signatureFilename
				source.files[path][0] ^= 0xff
				return source
			},
			wantedError: releasecontract.ErrSignatureInvalid,
		},
		{
			name: "artifact digest differs",
			mutate: func(source *memorySource, tag, filename string) Source {
				path := tag + "/" + filename
				source.files[path][0] ^= 0xff
				return source
			},
			wantedError: releasecontract.ErrArtifactMismatch,
		},
		{
			name: "artifact reaches early EOF",
			mutate: func(source *memorySource, tag, filename string) Source {
				path := tag + "/" + filename
				source.files[path] = bytes.Clone(source.files[path][:len(source.files[path])-1])
				return source
			},
			wantedError: releasecontract.ErrArtifactMismatch,
		},
		{
			name: "artifact read is interrupted",
			mutate: func(source *memorySource, _ string, filename string) Source {
				return &interruptedSource{delegate: source, filename: filename}
			},
			wantedError: ErrDownload,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temporary := t.TempDir()
			root := filepath.Join(temporary, "application-data")
			cache := filepath.Join(temporary, "plugin-cache")
			if err := os.Mkdir(cache, 0o700); err != nil {
				t.Fatal(err)
			}
			key := newSigningFixture(t)
			source := &memorySource{}
			goodPayload := []byte("last-known-good helper")
			goodTag := key.addRelease(t, source, "0.1.0-s3", goodPayload)
			candidateTag := key.addRelease(t, source, "0.1.0-s4", []byte("candidate helper payload"))
			candidateFilename := expectedFilename("0.1.0-s4", "macos-arm64")
			tester := &fakeSelfTester{}
			good, err := Install(context.Background(), configFor(root, cache, goodTag, source, key, tester))
			if err != nil {
				t.Fatal(err)
			}
			currentPath := filepath.Join(root, "bin", "current.json")
			currentBefore, err := os.ReadFile(currentPath)
			if err != nil {
				t.Fatal(err)
			}
			callsBefore := tester.calls
			candidateSource := test.mutate(source, candidateTag, candidateFilename)
			candidateConfig := configFor(root, cache, candidateTag, candidateSource, key, tester)
			if _, err := Install(context.Background(), candidateConfig); !errors.Is(err, test.wantedError) {
				t.Fatalf("wanted %v, got %v", test.wantedError, err)
			}
			if tester.calls != callsBefore {
				t.Fatal("untrusted candidate reached the executable self-test")
			}
			currentAfter, err := os.ReadFile(currentPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(currentBefore, currentAfter) {
				t.Fatal("failed candidate changed current.json")
			}
			installedPayload, err := os.ReadFile(good.Executable)
			if err != nil || !bytes.Equal(installedPayload, goodPayload) {
				t.Fatalf("last-known-good Helper changed: %q, %v", installedPayload, err)
			}
			if _, err := os.Stat(filepath.Join(root, "bin", "0.1.0-s4")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed candidate created a version directory: %v", err)
			}
			staging, err := filepath.Glob(filepath.Join(root, "bin", ".staging-*"))
			if err != nil || len(staging) != 0 {
				t.Fatalf("failed candidate left staging directories: %v, %v", staging, err)
			}
			reused, err := Install(context.Background(), configFor(root, cache, goodTag, source, key, tester))
			if err != nil || !reused.Reused || reused.Executable != good.Executable {
				t.Fatalf("last-known-good Helper was not reusable: %#v, %v", reused, err)
			}
		})
	}
}

func TestHTTPReleaseSourceRejectsInterruptedAndTruncatedBodies(t *testing.T) {
	tests := []struct {
		name          string
		body          io.ReadCloser
		contentLength int64
	}{
		{
			name:          "reader error after partial bytes",
			body:          &interruptedReadCloser{content: []byte("partial")},
			contentLength: -1,
		},
		{
			name:          "declared length exceeds body",
			body:          io.NopCloser(bytes.NewReader([]byte("short"))),
			contentLength: 9,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    http.StatusOK,
					Body:          test.body,
					ContentLength: test.contentLength,
					Request:       request,
					Header:        make(http.Header),
				}, nil
			})
			source := newHTTPReleaseSource(&http.Client{Transport: transport})
			content, err := source.Fetch(context.Background(), "helper-v0.1.0-s3", descriptorFilename, 64)
			if err == nil {
				t.Fatal("partial HTTP body was accepted")
			}
			if content != nil {
				t.Fatalf("partial HTTP bytes escaped the source: %q", content)
			}
		})
	}
}

func TestDowngradePreservesReusableLastKnownGood(t *testing.T) {
	temporary := t.TempDir()
	root := filepath.Join(temporary, "application-data")
	cache := filepath.Join(temporary, "plugin-cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	key := newSigningFixture(t)
	source := &memorySource{}
	newTag := key.addRelease(t, source, "0.2.0", []byte("new helper"))
	oldTag := key.addRelease(t, source, "0.1.0", []byte("old helper"))
	tester := &fakeSelfTester{}
	installed, err := Install(context.Background(), configFor(root, cache, newTag, source, key, tester))
	if err != nil {
		t.Fatal(err)
	}
	pointerBefore, err := os.ReadFile(filepath.Join(root, "bin", "current.json"))
	if err != nil {
		t.Fatal(err)
	}
	callsBefore := tester.calls
	if _, err := Install(context.Background(), configFor(root, cache, oldTag, source, key, tester)); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("downgrade was not rejected: %v", err)
	}
	if tester.calls != callsBefore {
		t.Fatal("downgrade candidate reached executable self-test")
	}
	pointerAfter, err := os.ReadFile(filepath.Join(root, "bin", "current.json"))
	if err != nil || !bytes.Equal(pointerBefore, pointerAfter) {
		t.Fatalf("downgrade changed current.json: %v", err)
	}
	reused, err := Install(context.Background(), configFor(root, cache, newTag, source, key, tester))
	if err != nil || !reused.Reused || reused.Executable != installed.Executable {
		t.Fatalf("last-known-good version was not reusable: %#v, %v", reused, err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("downgrade created an old version directory: %v", err)
	}
}

func TestInterruptedSourceReturnsPartialBytesAndError(t *testing.T) {
	source := &memorySource{files: map[string][]byte{"tag/file": []byte("payload")}}
	interrupted := &interruptedSource{delegate: source, filename: "file"}
	content, err := interrupted.Fetch(context.Background(), "tag", "file", 64)
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(content) == 0 {
		t.Fatalf("interruption fixture is ineffective: %q, %v", content, err)
	}
}
