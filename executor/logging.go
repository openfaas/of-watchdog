package executor

import (
	"bufio"
	"io"
	"log"
)

// bindLoggingPipe spawns a goroutine for passing through logging of the given output pipe.
func bindLoggingPipe(name string, pipe io.Reader, output io.Writer) {
	log.Printf("Started logging %s from function.", name)

	scanner := bufio.NewScanner(pipe)
	logger := log.New(output, log.Prefix(), log.Flags())

	go func() {
		for scanner.Scan() {
			logger.Printf("%s: %s", name, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			log.Printf("Error scanning %s: %s", name, err.Error())
		}
	}()
}
