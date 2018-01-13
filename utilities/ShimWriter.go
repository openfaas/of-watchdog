package utilities

import (
	"net/http"
)

type shimWriter struct {
	Nested http.ResponseWriter
	controlChan chan HTTPControl
	dirty bool
}

func NewShim(nest http.ResponseWriter, control string) http.ResponseWriter {
	ch := make(chan HTTPControl)
	go ReadFromPipe(control, ch)
	return shimWriter{nest, ch, false}
}

func (s shimWriter) Header() http.Header {
	return s.Nested.Header()
}

func (s shimWriter) WriteHeader(rc int) {
	s.dirty = true
	s.Nested.WriteHeader(rc)
}

func (s shimWriter) Write(bytes []byte) (int, error) {

	if s.dirty == false {
		select {
		case controlMessage := <-s.controlChan:
			// Write headers from the control message
			headers := s.Header()
			for key, value := range controlMessage.Headers {
				headers.Set(key, value)
			}
			if controlMessage.Status != 0 {
				s.WriteHeader(controlMessage.Status)
			} else {
				s.WriteHeader(200)
			}
		default:
			// If nothing was sent yet, explicitely send a 200 header
			s.WriteHeader(200)
		}
		s.dirty = true
	}

	return s.Nested.Write(bytes)
}
