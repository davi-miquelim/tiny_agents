package http

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	stdHttp "net/http"
	"net/http/httptest"
)

type SpellStub struct {
	Name   string `json:"Name"`
	Damage uint8  `json:"Damage"`
}

type SpellXML struct {
	XMLName xml.Name `xml:"Spell"`
	Name    string   `xml:"Name"`
	Damage  uint8    `xml:"Damage"`
}

func TestMutateJSON(t *testing.T) {
	spell := SpellStub{Name: "Nîn o Chithaeglir", Damage: 100}

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		if r.Method != stdHttp.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var got SpellStub
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if got.Name != spell.Name || got.Damage != spell.Damage {
			t.Errorf("body = %+v, want %+v", got, spell)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SpellStub{Name: got.Name, Damage: got.Damage + 1})
	}))
	defer srv.Close()

	res, err := Mutate[SpellStub, SpellStub](context.Background(), MutationParams[SpellStub]{
		Method:      POST,
		Url:         srv.URL,
		Body:        spell,
		ContentType: "application/json",
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if res.Status != stdHttp.StatusOK {
		t.Errorf("Status = %d, want 200", res.Status)
	}
	if res.Value.Name != spell.Name || res.Value.Damage != spell.Damage+1 {
		t.Errorf("Value = %+v", res.Value)
	}
}

func TestMutateJSONDefaultContentType(t *testing.T) {
	spell := SpellStub{Name: "Andúril", Damage: 50}

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(spell)
	}))
	defer srv.Close()

	res, err := Mutate[SpellStub, SpellStub](context.Background(), MutationParams[SpellStub]{
		Method: POST,
		Url:    srv.URL,
		Body:   spell,
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if res.Value != spell {
		t.Errorf("Value = %+v, want %+v", res.Value, spell)
	}
}

func TestMutateXML(t *testing.T) {
	spell := SpellXML{Name: "Glamdring", Damage: 80}

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/xml" {
			t.Errorf("Content-Type = %q, want application/xml", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var got SpellXML
		if err := xml.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(got)
	}))
	defer srv.Close()

	res, err := Mutate[SpellXML, SpellXML](context.Background(), MutationParams[SpellXML]{
		Method:      PATCH,
		Url:         srv.URL,
		Body:        spell,
		ContentType: "application/xml",
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if res.Value.Name != spell.Name || res.Value.Damage != spell.Damage {
		t.Errorf("Value = %+v, want %+v", res.Value, spell)
	}
}

func TestMutateGob(t *testing.T) {
	spell := SpellStub{Name: "Sting", Damage: 40}

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/gob" {
			t.Errorf("Content-Type = %q, want application/gob", ct)
		}
		var got SpellStub
		if err := gob.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/gob")
		if err := gob.NewEncoder(w).Encode(got); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	res, err := Mutate[SpellStub, SpellStub](context.Background(), MutationParams[SpellStub]{
		Method:      POST,
		Url:         srv.URL,
		Body:        spell,
		ContentType: "application/gob",
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if res.Value.Name != spell.Name || res.Value.Damage != spell.Damage {
		t.Errorf("Value = %+v, want %+v", res.Value, spell)
	}
}

func TestMutateForm(t *testing.T) {
	form := url.Values{"name": {"Narsil"}, "damage": {"90"}}

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		got, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got.Get("name") != "Narsil" || got.Get("damage") != "90" {
			t.Errorf("form = %v", got)
		}
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = io.WriteString(w, got.Encode())
	}))
	defer srv.Close()

	res, err := Mutate[url.Values, url.Values](context.Background(), MutationParams[url.Values]{
		Method:      POST,
		Url:         srv.URL,
		Body:        form,
		ContentType: "application/x-www-form-urlencoded",
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if res.Value.Get("name") != "Narsil" || res.Value.Get("damage") != "90" {
		t.Errorf("Value = %v", res.Value)
	}
}

func TestMutateUnsupportedContentType(t *testing.T) {
	_, err := Mutate[SpellStub, SpellStub](context.Background(), MutationParams[SpellStub]{
		Method:      POST,
		Url:         "http://example.com",
		Body:        SpellStub{Name: "x", Damage: 1},
		ContentType: "text/plain",
	})
	if err == nil {
		t.Fatal("expected error for unsupported content type")
	}
	if !strings.Contains(err.Error(), "unsupported content type") {
		t.Errorf("error = %q, want unsupported content type", err)
	}
}

func TestMutateFormNonFormBody(t *testing.T) {
	_, err := Mutate[SpellStub, url.Values](context.Background(), MutationParams[SpellStub]{
		Method:      POST,
		Url:         "http://example.com",
		Body:        SpellStub{Name: "x", Damage: 1},
		ContentType: "application/x-www-form-urlencoded",
	})
	if err == nil {
		t.Fatal("expected error for non-form body")
	}
	if !strings.Contains(err.Error(), "form encode requires") {
		t.Errorf("error = %q, want form encode requires", err)
	}
}

type roundTripFunc func(*stdHttp.Request) (*stdHttp.Response, error)

func (f roundTripFunc) RoundTrip(r *stdHttp.Request) (*stdHttp.Response, error) {
	return f(r)
}

func TestWithDefaultTimeoutAddsDeadline(t *testing.T) {
	before := time.Now()
	ctx, cancel := withDefaultTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("context has no deadline")
	}
	if got := deadline.Sub(before); got < defaultClientTimeout-time.Second || got > defaultClientTimeout+time.Second {
		t.Errorf("deadline is in %v, want approximately %v", got, defaultClientTimeout)
	}
}

func TestWithDefaultTimeoutPreservesCallerDeadline(t *testing.T) {
	want := time.Now().Add(time.Minute)
	ctx, callerCancel := context.WithDeadline(context.Background(), want)
	defer callerCancel()

	got, cancel := withDefaultTimeout(ctx)
	defer cancel()

	if got != ctx {
		t.Error("withDefaultTimeout replaced the caller context")
	}
	deadline, ok := got.Deadline()
	if !ok {
		t.Fatal("context has no deadline")
	}
	if !deadline.Equal(want) {
		t.Errorf("deadline = %v, want %v", deadline, want)
	}
}

func TestMutateRespectsCallerDeadline(t *testing.T) {
	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Name":"late","Damage":1}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := Mutate[SpellStub, SpellStub](ctx, MutationParams[SpellStub]{
		Method: POST,
		Url:    srv.URL,
		Body:   SpellStub{Name: "request"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Mutate error = %v, want context deadline exceeded", err)
	}
}

func TestMutateEmptyResponseContentTypeFallsBack(t *testing.T) {
	spell := SpellStub{Name: "Orcrist", Damage: 70}
	payload, err := json.Marshal(spell)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	old := client
	t.Cleanup(func() { client = old })
	client = &stdHttp.Client{
		Transport: roundTripFunc(func(r *stdHttp.Request) (*stdHttp.Response, error) {
			return &stdHttp.Response{
				StatusCode: stdHttp.StatusOK,
				Header:     make(stdHttp.Header), // no Content-Type
				Body:       io.NopCloser(bytes.NewReader(payload)),
			}, nil
		}),
	}

	res, err := Mutate[SpellStub, SpellStub](context.Background(), MutationParams[SpellStub]{
		Method:      POST,
		Url:         "http://example.com",
		Body:        spell,
		ContentType: "application/json",
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if res.Value.Name != spell.Name || res.Value.Damage != spell.Damage {
		t.Errorf("Value = %+v, want %+v", res.Value, spell)
	}
}

func TestGetJSON(t *testing.T) {
	spell := SpellStub{Name: "Nîn o Chithaeglir", Damage: 100}

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		if r.Method != stdHttp.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(spell)
	}))
	defer srv.Close()

	res, err := Get[SpellStub](context.Background(), GetParams{Url: srv.URL})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.Status != stdHttp.StatusOK {
		t.Errorf("Status = %d, want 200", res.Status)
	}
	if res.Value.Name != spell.Name || res.Value.Damage != spell.Damage {
		t.Errorf("Value = %+v, want %+v", res.Value, spell)
	}
}

func TestGetEmptyResponseContentTypeDefaultsToJSON(t *testing.T) {
	spell := SpellStub{Name: "Andúril", Damage: 50}
	payload, err := json.Marshal(spell)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	old := client
	t.Cleanup(func() { client = old })
	client = &stdHttp.Client{
		Transport: roundTripFunc(func(r *stdHttp.Request) (*stdHttp.Response, error) {
			return &stdHttp.Response{
				StatusCode: stdHttp.StatusOK,
				Header:     make(stdHttp.Header),
				Body:       io.NopCloser(bytes.NewReader(payload)),
			}, nil
		}),
	}

	res, err := Get[SpellStub](context.Background(), GetParams{Url: "http://example.com"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.Value.Name != spell.Name || res.Value.Damage != spell.Damage {
		t.Errorf("Value = %+v, want %+v", res.Value, spell)
	}
}

func TestGetQuery(t *testing.T) {
	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		if got := r.URL.Query().Get("q"); got != "x" {
			t.Errorf("query q = %q, want x", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SpellStub{Name: "ok", Damage: 1})
	}))
	defer srv.Close()

	_, err := Get[SpellStub](context.Background(), GetParams{
		Url:   srv.URL + "?ignored=1",
		Query: url.Values{"q": {"x"}},
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func TestGetUnsupportedContentType(t *testing.T) {
	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "nope")
	}))
	defer srv.Close()

	_, err := Get[SpellStub](context.Background(), GetParams{Url: srv.URL})
	if err == nil {
		t.Fatal("expected error for unsupported content type")
	}
	if !strings.Contains(err.Error(), "unsupported content type") {
		t.Errorf("error = %q, want unsupported content type", err)
	}
}

func TestGetNon2xx(t *testing.T) {
	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		w.WriteHeader(stdHttp.StatusNotFound)
		_, _ = io.WriteString(w, "missing")
	}))
	defer srv.Close()

	_, err := Get[SpellStub](context.Background(), GetParams{Url: srv.URL})
	if err == nil {
		t.Fatal("expected error for non-2xx")
	}
	if !strings.Contains(err.Error(), "unexpected status 404") {
		t.Errorf("error = %q, want unexpected status 404", err)
	}
}

func TestMutateStreamHappyPath(t *testing.T) {
	chunks := []SpellStub{
		{Name: "Sting", Damage: 1},
		{Name: "Sting", Damage: 2},
		{Name: "Sting", Damage: 3},
	}

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		flusher, ok := w.(stdHttp.Flusher)
		if !ok {
			t.Fatal("ResponseWriter is not a Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(stdHttp.StatusOK)
		_, _ = io.WriteString(w, ": OPENROUTER PROCESSING\n\n")
		flusher.Flush()
		for _, c := range chunks {
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("marshal chunk: %v", err)
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	var got []SpellStub
	err := MutateStream(context.Background(), MutationParams[SpellStub]{
		Method: POST,
		Url:    srv.URL,
		Body:   SpellStub{Name: "Sting", Damage: 0},
	}, func(out SpellStub) error {
		got = append(got, out)
		return nil
	})
	if err != nil {
		t.Fatalf("MutateStream: %v", err)
	}
	if len(got) != len(chunks) {
		t.Fatalf("got %d chunks, want %d", len(got), len(chunks))
	}
	for i := range chunks {
		if got[i] != chunks[i] {
			t.Errorf("chunk[%d] = %+v, want %+v", i, got[i], chunks[i])
		}
	}
}

func TestMutateStreamUsesLongContextTimeout(t *testing.T) {
	if client.Timeout != 0 {
		t.Fatalf("client.Timeout = %v, want 0 (streams use context, not Client.Timeout)", client.Timeout)
	}

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		flusher := w.(stdHttp.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(stdHttp.StatusOK)
		flusher.Flush()

		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, `data: {"Name":"late","Damage":1}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	var got []SpellStub
	err := MutateStream(context.Background(), MutationParams[SpellStub]{
		Method: POST,
		Url:    srv.URL,
		Body:   SpellStub{Name: "request"},
	}, func(out SpellStub) error {
		got = append(got, out)
		return nil
	})
	if err != nil {
		t.Fatalf("MutateStream: %v", err)
	}
	if len(got) != 1 || got[0].Name != "late" {
		t.Errorf("chunks = %+v, want late chunk", got)
	}
}

func TestRetryWaitCapsRetryAfter(t *testing.T) {
	if got := retryWait(0, "3600"); got != maxRetryWait {
		t.Errorf("retryWait(0, 3600) = %v, want %v", got, maxRetryWait)
	}
	if got := retryWait(0, "5"); got != 5*time.Second {
		t.Errorf("retryWait(0, 5) = %v, want 5s", got)
	}
	if got := retryWait(0, ""); got != retryBaseWait {
		t.Errorf("retryWait(0, \"\") = %v, want %v", got, retryBaseWait)
	}
}

func TestWithRetriesRetriesTransportError(t *testing.T) {
	var calls atomic.Int32
	old := client
	t.Cleanup(func() { client = old })
	client = &stdHttp.Client{
		Transport: roundTripFunc(func(r *stdHttp.Request) (*stdHttp.Response, error) {
			if calls.Add(1) == 1 {
				return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
			}
			payload, _ := json.Marshal(SpellStub{Name: "ok", Damage: 1})
			return &stdHttp.Response{
				StatusCode: stdHttp.StatusOK,
				Header:     stdHttp.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(payload)),
			}, nil
		}),
	}

	res, err := Mutate[SpellStub, SpellStub](context.Background(), MutationParams[SpellStub]{
		Method: POST,
		Url:    "http://example.com",
		Body:   SpellStub{Name: "x", Damage: 1},
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
	if res.Value.Name != "ok" {
		t.Errorf("Value = %+v, want ok", res.Value)
	}
}

func TestWithRetriesRetries502(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(stdHttp.StatusBadGateway)
			_, _ = io.WriteString(w, "bad gateway")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SpellStub{Name: "ok", Damage: 1})
	}))
	defer srv.Close()

	res, err := Mutate[SpellStub, SpellStub](context.Background(), MutationParams[SpellStub]{
		Method: POST,
		Url:    srv.URL,
		Body:   SpellStub{Name: "x", Damage: 1},
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
	if res.Value.Name != "ok" {
		t.Errorf("Value = %+v, want ok", res.Value)
	}
}

func TestMutateStreamPreStreamError(t *testing.T) {
	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		w.WriteHeader(stdHttp.StatusBadRequest)
		_, _ = io.WriteString(w, "bad request body")
	}))
	defer srv.Close()

	called := false
	err := MutateStream(context.Background(), MutationParams[SpellStub]{
		Method: POST,
		Url:    srv.URL,
		Body:   SpellStub{Name: "x", Damage: 1},
	}, func(out SpellStub) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected error for non-2xx")
	}
	if !strings.Contains(err.Error(), "unexpected status 400") {
		t.Errorf("error = %q, want unexpected status 400", err)
	}
	if called {
		t.Error("onChunk was called on error response")
	}
}

func TestMutateStreamContextCancel(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once

	srv := httptest.NewServer(stdHttp.HandlerFunc(func(w stdHttp.ResponseWriter, r *stdHttp.Request) {
		flusher, ok := w.(stdHttp.Flusher)
		if !ok {
			t.Fatal("ResponseWriter is not a Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(stdHttp.StatusOK)

		data, _ := json.Marshal(SpellStub{Name: "first", Damage: 1})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		once.Do(func() { close(started) })

		// Keep writing slowly until the client disconnects / context cancels.
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				data, _ := json.Marshal(SpellStub{Name: "more", Damage: 2})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	err := MutateStream(ctx, MutationParams[SpellStub]{
		Method: POST,
		Url:    srv.URL,
		Body:   SpellStub{Name: "x", Damage: 1},
	}, func(out SpellStub) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %q, want context canceled", err)
	}
}
