package executor

import (
	"bufio"
	"io"
	"log"
	"time"
)

var stdLogBufferSize = 64 * 1024

// bindLoggingPipe spawns a goroutine for passing through logging of the given output pipe.
//
// This implementation is adapted from https://github.com/hashicorp/go-plugin/pull/98
func bindLoggingPipe(name string, pipe io.Reader, output io.Writer) {
	log.Printf("Started logging %s from function.", name)

	scanner := bufio.NewReaderSize(pipe, stdLogBufferSize)

	go func() {
		logFlags := log.Flags()
		continuation := false

		for {
			line, isPrefix, err := scanner.ReadLine()
			switch {
			case err == io.EOF:
				return
			case err != nil:
				log.Printf("Error scanning %s: %s", name, err.Error())
				return
			}

			if !continuation {
				// we are not continuing a previous line, so we
				// start the log line with the log prefix
				output.Write(logHeader(logFlags))
			}

			output.Write(line)
			// the line is longer than stdLogBufferSize
			if isPrefix || continuation {

				// we are continuing a long log line,
				// but it is the end of the actual line,
				// so put the newline back in
				if !isPrefix {
					output.Write([]byte{'\n'})
				}

				continuation = isPrefix
				continue
			}

			// not a prefix or continuation, so we have written a complete line
			// so put the newline back in
			output.Write([]byte{'\n'})
		}
	}()
}

// logHeader copies timestamp construction from the log package `formatHeader`
// this is needed to preserve backwards compatibility while also allowing us
// to control when newlines are added to the output. By default, all print
// statements to the logger will suffix a newline (if it is missing). This
// prevents us from writing very long lines to the output.
//
// Note that this does not copy the support for the following log flags
// * Lshortfile
// * Llongfile
// * Lmsgprefix
//
// The first two don't really make sense for the of-watchdog because we
// don't know the file location of the log statement in the actual function
// implementation.  The second is not implemented because it was never
// used previously.
//
// Supporting the flags makes the unit testing easier.
func logHeader(flag int) []byte {
	buf := []byte{}
	t := time.Now()
	if flag&(log.Ldate|log.Ltime|log.Lmicroseconds) != 0 {
		if flag&log.LUTC != 0 {
			t = t.UTC()
		}
		if flag&log.Ldate != 0 {
			year, month, day := t.Date()
			itoa(&buf, year, 4)
			buf = append(buf, '/')
			itoa(&buf, int(month), 2)
			buf = append(buf, '/')
			itoa(&buf, day, 2)
			buf = append(buf, ' ')
		}
		if flag&(log.Ltime|log.Lmicroseconds) != 0 {
			hour, min, sec := t.Clock()
			itoa(&buf, hour, 2)
			buf = append(buf, ':')
			itoa(&buf, min, 2)
			buf = append(buf, ':')
			itoa(&buf, sec, 2)
			if flag&log.Lmicroseconds != 0 {
				buf = append(buf, '.')
				itoa(&buf, t.Nanosecond()/1e3, 6)
			}
			buf = append(buf, ' ')
		}
	}
	return buf
}

// Cheap integer to fixed-width decimal ASCII. Give a negative width to avoid zero-padding.
func itoa(buf *[]byte, i int, wid int) {
	// Assemble decimal in reverse order.
	var b [20]byte
	bp := len(b) - 1
	for i >= 10 || wid > 1 {
		wid--
		q := i / 10
		b[bp] = byte('0' + i - q*10)
		bp--
		i = q
	}
	// i < 10
	b[bp] = byte('0' + i)
	*buf = append(*buf, b[bp:]...)
}
