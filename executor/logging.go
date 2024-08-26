// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package executor

import (
	"bufio"
	"io"
	"log"
)

// bindLoggingPipe spawns a goroutine for passing through logging of the given output pipe.
func bindLoggingPipe(name string, pipe io.Reader, output io.Writer, logPrefix bool, maxBufferSize int) {
	log.Printf("Started logging: %s from function.", name)

	logFlags := log.Flags()
	prefix := log.Prefix()
	if logPrefix == false {
		logFlags = 0
		prefix = "" // Unnecessary, but set explicitly for completeness.
	}

	logger := log.New(output, prefix, logFlags)

	if maxBufferSize >= 0 {
		go pipeBuffered(name, pipe, logger, logPrefix, maxBufferSize)
	} else {
		go pipeUnbuffered(name, pipe, logger, logPrefix)
	}
}

func pipeBuffered(name string, pipe io.Reader, logger *log.Logger, logPrefix bool, maxBufferSize int) {
	buf := make([]byte, maxBufferSize)
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(buf, maxBufferSize)

	for scanner.Scan() {
		if logPrefix {
			logger.Printf("%s: %s", name, scanner.Text())
		} else {
			logger.Print(scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading %s: %s", name, err)
	}
}

func pipeUnbuffered(name string, pipe io.Reader, logger *log.Logger, logPrefix bool) {

	r := bufio.NewReader(pipe)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading %s: %s", name, err)
			}
			break
		}
		if logPrefix {
			logger.Printf("%s: %s", name, line)
		} else {
			logger.Print(line)
		}
	}

}
