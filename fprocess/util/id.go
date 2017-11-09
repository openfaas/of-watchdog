package util

import "fmt"

var reqIDs = make(chan uint64)

func init() {
	go func() {
		for i := uint64(0); ; i++ {
			reqIDs <- i
		}
	}()
}

func NewReqID() string {
	return fmt.Sprint(<-reqIDs)
}
