package tests

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

var testDataPath string

func init() {
	pwd, _ := os.Getwd()
	testDataPath = filepath.Join(pwd, ".testdata")
}

// GetTestFileContent return test file content.
func GetTestFileContent(t *testing.T, filename string) []byte {
	b, err := ioutil.ReadFile(GetTestFilePath(filename))
	AssertNoError(t, err)
	return b
}

// GetTestFilePath return test file absolute path.
func GetTestFilePath(filename string) string {
	return filepath.Join(testDataPath, filename)
}
