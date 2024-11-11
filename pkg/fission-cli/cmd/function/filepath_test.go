package function

import(
	"testing"
	"fmt"
    // "log"
    "path/filepath"
)

func  TestFilepath(t *testing.T) {

	fname := "test.yml"
    
	files, _ := filepath.Glob(fname)

    fmt.Println(len(files))
}