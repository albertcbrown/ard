// Command imagescan reads `docker export` output on stdin and fails when the
// image's filesystem holds a credential (internal/imagescan). Used by
// deploy/test-image.sh and deploy/test-agent-image.sh:
//
//	docker export <container> | go run ./deploy/imagescan
//
// Exit 0 clean, 1 findings (printed, never the matched text), 2 the scan
// could not finish.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/albertcbrown/ard/internal/imagescan"
)

func main() {
	fs, err := imagescan.Scan(context.Background(), bufio.NewReaderSize(os.Stdin, 1<<20), imagescan.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "imagescan:", err)
		os.Exit(2)
	}
	for _, f := range fs {
		fmt.Println(f)
	}
	if len(fs) > 0 {
		fmt.Fprintf(os.Stderr, "imagescan: %d finding(s)\n", len(fs))
		os.Exit(1)
	}
}
