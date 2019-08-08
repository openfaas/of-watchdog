package executor

import (
	"bufio"
	"io"
	"log"
)

// bindLoggingPipe spawns a goroutine for passing through logging of the given output pipe.
func bindLoggingPipe(name string, output io.Reader) {
	log.Printf("Started logging %s from function.", name)

	scanner := bufio.NewScanner(output)

	go func() {
		for scanner.Scan() {
			log.Printf("%s: %s", name, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			log.Printf("Error scanning %s: %s", name, err.Error())
		}
	}()
}
