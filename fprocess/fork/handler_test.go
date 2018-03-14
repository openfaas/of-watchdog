package fork

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/openfaas-incubator/of-watchdog/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_HandleRun_logStderr(t *testing.T) {
	wg := &sync.WaitGroup{}
	pr, pw := io.Pipe()
	wg.Add(1)
	go func() {
		s := bufio.NewScanner(pr)
		for s.Scan() {
			t.Logf("stderr: %s", s.Text())
		}
		wg.Done()
	}()
	fmt.Fprintln(pw, "loggy log")
	fmt.Fprintln(pw, "loggy log")
	pw.Close()
	wg.Wait()
}

type testResponse struct {
	*httptest.ResponseRecorder
	sync.WaitGroup
}

func newTestResponse() *testResponse {
	return &testResponse{ResponseRecorder: httptest.NewRecorder()}
}

func (tr *testResponse) Flush() {
	tr.ResponseRecorder.Flush()
	tr.Done()
}

func TestHandler_HandleRun_expectStderr(t *testing.T) {
	os.Setenv("mode", "fork")
	os.Setenv("fprocess", "tee /dev/stderr")
	conf := config.New(os.Environ())

	wg := sync.WaitGroup{}

	handler := New(conf)

	orR, orW := io.Pipe()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer orW.Close()
		for i := 0; i < 10; i++ {
			fmt.Fprintf(orW, "in %v\n", i)
		}
	}()

	// stdout request
	or := httptest.NewRequest("POST", "/", orR)
	or.Header.Set("X-Expect", "Link-stderr")

	// stdout response
	ow := newTestResponse()
	ow.Add(1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		http.HandlerFunc(handler.HandleRun).ServeHTTP(ow, or)
	}()
	ow.Wait()

	linkHeader := ow.Header().Get("Link")
	require.Contains(t, linkHeader, `</stderr/`)
	require.Contains(t, linkHeader, `>; rel="stderr"`)
	reqID := strings.TrimPrefix(linkHeader, `</stderr/`)
	reqID = strings.TrimSuffix(reqID, `>; rel="stderr"`)
	reqID = strings.TrimSpace(reqID)
	require.NotEmpty(t, reqID)
	assert.Empty(t, ow.Header().Get("Expires"))
	assert.Empty(t, ow.Header().Get("X-Error"))

	// stderr request
	er := httptest.NewRequest("GET", fmt.Sprintf("/stderr/%s", reqID), nil)

	// stderr response
	ew := newTestResponse()

	wg.Add(1)
	go func() {
		defer wg.Done()
		http.HandlerFunc(handler.HandleStderr).ServeHTTP(ew, er)
	}()

	wg.Wait()

	assert.NotEmpty(t, ow.Body.Bytes())
	assert.NotEmpty(t, ew.Body.Bytes())
	assert.Equal(t, ow.Body.Bytes(), ew.Body.Bytes())
}

func TestHandler_HandleRun_e2e(t *testing.T) {
	os.Setenv("mode", "fork")
	os.Setenv("fprocess", "tee /dev/stderr")
	conf := config.New(os.Environ())

	handler := New(conf)

	wgEndOfInput := sync.WaitGroup{}
	defer wgEndOfInput.Wait()

	orR, orW := io.Pipe()
	wgEndOfInput.Add(1)
	go func() {
		defer wgEndOfInput.Done()
		defer orW.Close()
		for i := 0; i < 10; i++ {
			_, err := fmt.Fprintf(orW, "in %v\n", i)
			require.NoError(t, err)
		}
	}()

	http.HandleFunc("/", handler.HandleRun)
	http.HandleFunc("/stderr/", handler.HandleStderr)

	server := httptest.NewUnstartedServer(http.DefaultServeMux)
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.Config.TLSNextProto = nil
	server.StartTLS()

	defer server.Close()

	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig.NextProtos = []string{"h2"}

	// stdout request
	req, err := http.NewRequest("POST", server.URL, orR)
	require.NoError(t, err)
	req.ContentLength = -1
	req.Header.Set("X-Expect", "Link-stderr")

	// stdout response
	res, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, res.StatusCode)

	linkHeader := res.Header.Get("Link")
	require.Contains(t, linkHeader, `</stderr/`)
	require.Contains(t, linkHeader, `>; rel="stderr"`)
	reqID := strings.TrimPrefix(linkHeader, `</stderr/`)
	reqID = strings.TrimSuffix(reqID, `>; rel="stderr"`)
	reqID = strings.TrimSpace(reqID)
	require.NotEmpty(t, reqID)

	assert.Empty(t, res.Header.Get("Expires"))
	assert.Empty(t, res.Header.Get("X-Error"))

	// stderr response
	stderrRes, err := client.Get(fmt.Sprintf("%s/stderr/%s", server.URL, reqID))
	require.NoError(t, err)
	require.Equal(t, 200, stderrRes.StatusCode)

	assert.NotNil(t, res.Body)
	assert.NotNil(t, stderrRes.Body)

	wg := sync.WaitGroup{}
	defer wg.Wait()

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(res.Body)
		for scanner.Scan() {
			s := scanner.Text()
			t.Logf("stdout: %s\n", s)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrRes.Body)
		for scanner.Scan() {
			s := scanner.Text()
			t.Logf("stderr: %s\n", s)
		}
	}()
}
