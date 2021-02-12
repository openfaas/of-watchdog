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
	prefix := log.Prefix()
	if logPrefix == false {
		logFlags = 0
		prefix = "" // Unnecessary, but set explicitly for completeness.
	}

	logger := log.New(output, prefix, logFlags)

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
