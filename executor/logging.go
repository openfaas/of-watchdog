package executor

import (
	"bufio"
	"io"
	"log"
)

// bindLoggingPipe spawns a goroutine for passing through logging of the given output pipe.
func bindLoggingPipe(name string, pipe io.Reader, output io.Writer, logPrefix bool) {
	log.Printf("Started logging %s from function.", name)

	scanner := bufio.NewScanner(pipe)
	logFlags := log.Flags()
	if !logPrefix {
		logFlags = 0
	}

	logger := log.New(output, log.Prefix(), logFlags)

	go func() {
		for scanner.Scan() {
			if logPrefix {
				logger.Printf("%s: %s", name, scanner.Text())
			} else {
				logger.Printf(scanner.Text())
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("Error scanning %s: %s", name, err.Error())
		}
	}()
}
