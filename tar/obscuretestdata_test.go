package tar

import (
	"encoding/base64"
	"io"
	"os"
)

func decodeObscuredTestdataToTempFile(name string) (path string, err error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()

	tmp, err := os.CreateTemp("", "arkive-obscuretestdata-decoded-")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, base64.NewDecoder(base64.StdEncoding, f)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}
