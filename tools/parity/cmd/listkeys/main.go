// Command listkeys prints config.PublicKeys one per line (parity inventory).
package main

import (
	"fmt"

	"github.com/hilather/mount-wrapper/internal/config"
)

func main() {
	for _, k := range config.PublicKeys() {
		fmt.Println(k)
	}
}
