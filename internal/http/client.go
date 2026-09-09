// Package http is a small typed HTTP client for JSON and related encodings.
//
// # Supported Content-Types
//
// Request bodies (MutationParams.ContentType) and response bodies are encoded
// and decoded using the same media-type set. Parameters after ';' (e.g.
// charset) are ignored when selecting a codec.
//
//   - application/json (default when ContentType is empty)
//   - application/xml, text/xml
//   - application/gob (Go encoding/gob; not portable to non-Go clients)
//   - application/x-www-form-urlencoded (url.Values or string maps on encode;
//     *url.Values or *map[string][]string on decode)
//
// Mutate and Get decode the response using the response Content-Type header,
// falling back to the request Content-Type (Mutate) or application/json (Get)
// when the header is missing. MutateStream always reads text/event-stream with
// JSON payloads on data: lines (OpenAI-style), independent of ContentType.
package http

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/gob"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	stdHttp "net/http"
)

const (
	defaultClientTimeout = 30 * time.Second
	defaultStreamTimeout = 10 * time.Minute
	maxRetries           = 3
	retryBaseWait        = 500 * time.Millisecond
	maxRetryWait         = 60 * time.Second
	maxSSELine           = 1024 * 1024
)

var client = &stdHttp.Client{}

type Header struct {
	Name  string
	Value string
}

type HttpMethod string

const (
	POST  HttpMethod = "POST"
	PATCH HttpMethod = "PATCH"
)

const defaultContentType = "application/json"

// MutationParams configures a POST or PATCH request with a typed body.
type MutationParams[T any] struct {
	Method HttpMethod
	Url    string
	Body   T
	// ContentType selects request body encoding. Empty means application/json.
	// See package docs for the full supported set.
	ContentType string
	Headers     []Header
}

// GetParams configures a GET request.
type GetParams struct {
	Url     string
	Query   url.Values // if non-nil, replaces URL query; nil leaves URL query unchanged
	Headers []Header
}

func isHttpMethodValid(m HttpMethod) bool {
	switch m {
	case POST, PATCH:
		return true
	default:
		return false
	}
}

func isRetryableStatus(code int) bool {
	switch code {
	case 429, 502, 503, 504:
		return true
	default:
		return false
	}
}

func retryWait(attempt int, retryAfter string) time.Duration {
	d := retryBaseWait << attempt
	if retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs >= 0 {
			d = time.Duration(secs) * time.Second
		}
	}
	if d > maxRetryWait {
		return maxRetryWait
	}
	return d
}

func isRetryableError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func baseMediaType(contentType string) string {
	media, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(media)
}

type HttpResponse[T any] struct {
	Status int
	Value  T
}

func encodeGob(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeGob(r io.Reader, dest any) error {
	return gob.NewDecoder(r).Decode(dest)
}

func encodeForm(v any) ([]byte, error) {
	switch t := v.(type) {
	case url.Values:
		return []byte(t.Encode()), nil
	case map[string][]string:
		return []byte(url.Values(t).Encode()), nil
	case map[string]string:
		vals := make(url.Values, len(t))
		for k, val := range t {
			vals.Set(k, val)
		}
		return []byte(vals.Encode()), nil
	default:
		return nil, fmt.Errorf("form encode requires url.Values or string map, got %T", v)
	}
}

func decodeForm(r io.Reader, dest any) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return err
	}
	switch d := dest.(type) {
	case *url.Values:
		*d = vals
		return nil
	case *map[string][]string:
		*d = map[string][]string(vals)
		return nil
	default:
		return fmt.Errorf("form decode requires *url.Values or *map[string][]string, got %T", dest)
	}
}

func encodeBody(contentType string, v any) ([]byte, error) {
	switch baseMediaType(contentType) {
	case "application/json":
		return json.Marshal(v)
	case "application/xml", "text/xml":
		return xml.Marshal(v)
	case "application/gob":
		return encodeGob(v)
	case "application/x-www-form-urlencoded":
		return encodeForm(v)
	default:
		return nil, fmt.Errorf("unsupported content type for encode: %q", contentType)
	}
}

func decodeContent(contentType string, r io.Reader, dest any) error {
	switch baseMediaType(contentType) {
	case "application/json":
		return json.NewDecoder(r).Decode(dest)
	case "application/xml", "text/xml":
		return xml.NewDecoder(r).Decode(dest)
	case "application/gob":
		return decodeGob(r, dest)
	case "application/x-www-form-urlencoded":
		return decodeForm(r, dest)
	default:
		return fmt.Errorf("unsupported content type for decode: %q", contentType)
	}
}

func prepareMutationRequest[T any](ctx context.Context, p MutationParams[T]) (*stdHttp.Request, string, error) {
	if !isHttpMethodValid(p.Method) {
		return nil, "", fmt.Errorf("invalid http method: %q", p.Method)
	}

	reqCT := cmp.Or(p.ContentType, defaultContentType)

	data, err := encodeBody(reqCT, p.Body)
	if err != nil {
		return nil, "", fmt.Errorf("could not encode body: %w", err)
	}

	req, err := stdHttp.NewRequestWithContext(ctx, string(p.Method), p.Url, bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("failed to initialize request: %w", err)
	}

	for _, header := range p.Headers {
		req.Header.Add(header.Name, header.Value)
	}
	req.Header.Set("Content-Type", reqCT)

	return req, reqCT, nil
}

func sleepOrDone(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return withTimeout(ctx, defaultClientTimeout)
}

// withRetries runs attempt until a non-retryable response/error or retries are exhausted.
// attempt must build a fresh request each call (bodies are not reusable).
func withRetries(ctx context.Context, attempt func() (*stdHttp.Response, error)) (*stdHttp.Response, error) {
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := attempt()
		if err != nil {
			if isRetryableError(err) && i < maxRetries {
				lastErr = err
				if err := sleepOrDone(ctx, retryWait(i, "")); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		if isRetryableStatus(res.StatusCode) && i < maxRetries {
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			lastErr = fmt.Errorf("unexpected status %d: %s", res.StatusCode, body)
			if err := sleepOrDone(ctx, retryWait(i, res.Header.Get("Retry-After"))); err != nil {
				return nil, err
			}
			continue
		}
		return res, nil
	}
	return nil, lastErr
}

func statusError(res *stdHttp.Response) error {
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return fmt.Errorf("unexpected status %d: unable to read response body: %w", res.StatusCode, err)
	}
	return fmt.Errorf("unexpected status %d: %s", res.StatusCode, body)
}

func decodeBody[Out any](res *stdHttp.Response, fallbackCT string) (*HttpResponse[Out], error) {
	resCT := cmp.Or(res.Header.Get("Content-Type"), fallbackCT)
	var result Out
	err := decodeContent(resCT, res.Body, &result)
	res.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}
	return &HttpResponse[Out]{Status: res.StatusCode, Value: result}, nil
}

// Mutate sends a typed mutation request and decodes a successful response body
// into Out using the supported Content-Types (see package docs).
func Mutate[In, Out any](ctx context.Context, p MutationParams[In]) (*HttpResponse[Out], error) {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()

	var reqCT string
	res, err := withRetries(ctx, func() (*stdHttp.Response, error) {
		req, ct, err := prepareMutationRequest(ctx, p)
		if err != nil {
			return nil, err
		}
		reqCT = ct
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error calling %q: %w", p.Url, err)
		}
		return res, nil
	})
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, statusError(res)
	}
	return decodeBody[Out](res, reqCT)
}

// MutateStream sends a typed mutation request and consumes an SSE response.
// Each data: line is JSON-decoded into Out; data: [DONE] ends the stream.
// Request ContentType still applies to the outbound body; the stream payload
// codec is always JSON.
func MutateStream[In, Out any](ctx context.Context, p MutationParams[In], onChunk func(Out) error) error {
	ctx, cancel := withTimeout(ctx, defaultStreamTimeout)
	defer cancel()

	res, err := withRetries(ctx, func() (*stdHttp.Response, error) {
		req, _, err := prepareMutationRequest(ctx, p)
		if err != nil {
			return nil, err
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error calling %q: %w", p.Url, err)
		}
		return res, nil
	})
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return statusError(res)
	}
	err = readSSE(res.Body, onChunk)
	res.Body.Close()
	return err
}

func readSSE[Out any](body io.Reader, onChunk func(Out) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				return nil
			}
			var out Out
			if err := json.Unmarshal([]byte(payload), &out); err != nil {
				return fmt.Errorf("error decoding SSE data: %w", err)
			}
			if err := onChunk(out); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// Get sends a GET request and decodes a successful response body into Out
// using the supported Content-Types (see package docs).
func Get[Out any](ctx context.Context, p GetParams) (*HttpResponse[Out], error) {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()

	u, err := url.Parse(p.Url)
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %w", p.Url, err)
	}
	if p.Query != nil {
		u.RawQuery = p.Query.Encode()
	}

	res, err := withRetries(ctx, func() (*stdHttp.Response, error) {
		req, err := stdHttp.NewRequestWithContext(ctx, stdHttp.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize request: %w", err)
		}
		for _, header := range p.Headers {
			req.Header.Add(header.Name, header.Value)
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error calling %q: %w", u.String(), err)
		}
		return res, nil
	})
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, statusError(res)
	}
	return decodeBody[Out](res, defaultContentType)
}
