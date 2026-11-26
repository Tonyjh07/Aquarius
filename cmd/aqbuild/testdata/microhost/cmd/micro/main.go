package main

import (
	"fmt"

	"example.com/localdep"
)

func main() {
	names := make([]string, 0, len(externalFactories()))
	for _, f := range externalFactories() {
		names = append(names, f.Name())
	}
	fmt.Printf("dep=%s factories=%d names=%v\n", localdep.Name(), len(names), names)
}
