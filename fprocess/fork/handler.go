package fork

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/openfaas-incubator/of-watchdog/config"
	"github.com/openfaas-incubator/of-watchdog/fprocess"
	"github.com/openfaas-incubator/of-watchdog/fprocess/util"
)

func New(config config.WatchdogConfig) fprocess.Handler {
	functionInvoker := ForkFunctionRunner{
		HardTimeout: config.HardTimeout,
	}
	return &handler{
		config:          config,
		functionInvoker: functionInvoker,
		req:             map[string]*fprocess.FunctionRequest{},
	}
}

type handler struct {
	config          config.WatchdogConfig
	functionInvoker ForkFunctionRunner

	sync.Mutex
	req map[string]*fprocess.FunctionRequest
}

func (h *handler) RmReq(reqID string) *fprocess.FunctionRequest {
	h.Lock()
	defer h.Unlock()
	defer delete(h.req, reqID)
	return h.req[reqID]
}

func (h *handler) SetReq(reqID string, req *fprocess.FunctionRequest) {
	h.Lock()
	defer h.Unlock()
	h.req[reqID] = req
}

func (h *handler) HandleRun(w http.ResponseWriter, r *http.Request) {

	var environment []string

	if h.config.InjectCGIHeaders {
		environment = getEnvironment(r)
	}

	commandName, arguments := h.config.Process()
	cmd := exec.Command(commandName, arguments...)
	cmd.Env = environment

	er, ew := io.Pipe()
	defer ew.Close()

	req := &fprocess.FunctionRequest{
		Cmd:          cmd,
		InputReader:  r.Body,
		OutputWriter: w,
		ErrorWriter:  ew,
		ErrorReader:  er,
		WaitErr:      make(chan error),
	}

	if r.Header.Get("Expect") == "Link-stderr" {
		h.runWithStderr(w, r, req)
	} else {
		h.run(w, r, req)
	}
}

func (h *handler) runWithStderr(w http.ResponseWriter, r *http.Request, req *fprocess.FunctionRequest) {
	reqID := util.NewReqID()
	h.SetReq(reqID, req)

	w.Header().Add("Link", fmt.Sprintf("</stderr/%s>; rel=\"stderr\"", reqID))
	w.Header().Add("Trailer", "Expires")
	w.Header().Add("Trailer", "X-Error")
	w.(http.Flusher).Flush() // otherwise panic :)

	time.AfterFunc(h.config.GetStderrTimeout, func() {
		defer func() {
			recover() // do not panic! if the channel is closed, discard the error
		}()
		h.RmReq(reqID)
		req.WaitErr <- fmt.Errorf("timed out waiting for stderr request")
	})

	if err := h.functionInvoker.Run(req); err != nil {
		io.WriteString(w, "\n")
		w.Header().Set("Expires", "0")
		w.Header().Set("X-Error", err.Error())
	}
}

func (h *handler) run(w http.ResponseWriter, r *http.Request, req *fprocess.FunctionRequest) {
	go func() {
		s := bufio.NewScanner(req.ErrorReader)
		for s.Scan() {
			log.Printf("stderr: %s", s.Text())
		}
	}()

	close(req.WaitErr) // unblock run

	err := h.functionInvoker.Run(req)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
	}
}

func (h *handler) getStderrHandler(reqID string) http.HandlerFunc {
	req := h.RmReq(reqID)
	if req == nil {
		return http.NotFound
	}
	return func(w http.ResponseWriter, r *http.Request) {
		close(req.WaitErr) // unblock run

		w.Header().Add("Trailer", "Expires")
		w.Header().Add("Trailer", "X-Error")

		if _, err := io.Copy(w, req.ErrorReader); err != nil {
			io.WriteString(w, "\n")
			w.Header().Set("Expires", "0")
			w.Header().Set("X-Error", err.Error())
		}
	}
}

func (h *handler) HandleStderr(w http.ResponseWriter, r *http.Request) {
	reqID := strings.TrimPrefix(r.URL.Path, "/stderr/")
	h.getStderrHandler(reqID)(w, r)
}

func getEnvironment(r *http.Request) []string {
	var envs []string

	envs = os.Environ()
	for k, v := range r.Header {
		kv := fmt.Sprintf("Http_%s=%s", strings.Replace(k, "-", "_", -1), v[0])
		envs = append(envs, kv)
	}
	envs = append(envs, fmt.Sprintf("Http_Method=%s", r.Method))

	if len(r.URL.RawQuery) > 0 {
		envs = append(envs, fmt.Sprintf("Http_Query=%s", r.URL.RawQuery))
	}

	if len(r.URL.Path) > 0 {
		envs = append(envs, fmt.Sprintf("Http_Path=%s", r.URL.Path))
	}

	return envs
}
