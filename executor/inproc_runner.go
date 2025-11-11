package executor

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	units "github.com/docker/go-units"
	"github.com/openfaas/faas-provider/httputil"
)

type InprocRunner struct {
	handler       http.HandlerFunc
	prefixLogs    bool
	logBufferSize int
	execTimeout   time.Duration
	logCallId     bool
}

func NewInprocRunner(handler http.HandlerFunc, prefixLogs bool, logBufferSize int, logCallId bool, execTimeout time.Duration) *InprocRunner {
	return &InprocRunner{
		handler:       handler,
		prefixLogs:    prefixLogs,
		logBufferSize: logBufferSize,
		logCallId:     logCallId,
		execTimeout:   execTimeout,
	}
}

func (inpr *InprocRunner) Start() error {
	return nil
}

func (inpr *InprocRunner) Run(w http.ResponseWriter, r *http.Request) error {

	ctx, cancel := context.WithTimeout(r.Context(), inpr.execTimeout)
	defer cancel()

	st := time.Now()
	ww := httputil.NewHttpWriteInterceptor(w)
	inpr.handler(ww, r.WithContext(ctx))

	done := time.Since(st)
	// Exclude logging for health check probes from the kubelet which can spam
	// log collection systems.
	if !strings.HasPrefix(r.UserAgent(), "kube-probe") {
		if inpr.logCallId {
			callId := r.Header.Get("X-Call-Id")
			if callId == "" {
				callId = "none"
			}

			log.Printf("%s %s - %d - ContentLength: %s (%.4fs) [%s]", r.Method, r.RequestURI, ww.Status(), units.HumanSize(float64(ww.BytesWritten())), done.Seconds(), callId)
		} else {
			log.Printf("%s %s - %d - ContentLength: %s (%.4fs)", r.Method, r.RequestURI, ww.Status(), units.HumanSize(float64(ww.BytesWritten())), done.Seconds())
		}
	}

	return nil
}
