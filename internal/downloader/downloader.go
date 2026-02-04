package downloader

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// callback types
type ProgressFunc func(n int64)
type DoneFunc func()
type ErrorFunc func(err error)

func Download(reqUrl string, onProgress ProgressFunc, onDone DoneFunc, onError ErrorFunc) {
	u, err := url.Parse(reqUrl)
	if err != nil {
		onError(err)
		return
	}

	req, err := http.NewRequest("GET", reqUrl, nil)
	if err != nil {
		onError(err)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		onError(err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		onError(fmt.Errorf("bad status: %s", resp.Status))
		return
	}

	filename := getFileName(u, resp)

	file, err := os.Create(filename)
	if err != nil {
		onError(err)
		return
	}
	defer file.Close()

	_, err = copy(resp.Body, file, onProgress)
	if err != nil {
		onError(err)
		return
	}

	onDone()
}

// <== Helper Functions ==>
func getFileName(u *url.URL, resp *http.Response) string {
	contentDisposition := resp.Header.Get("Content-Disposition")

	if contentDisposition != "" {
		_, params, err := mime.ParseMediaType(contentDisposition)
		if err == nil {
			if fname, ok := params["filename"]; ok {
				return fname
			}
			if fname, ok := params["filename*"]; ok {
				return fname
			}
		}
	}
	pathSlice := strings.Split(u.Path, "/")
	return pathSlice[len(pathSlice)-1]
}

// copy with progress callback
func copy(src io.Reader, dst io.Writer, onProgress ProgressFunc) (int64, error) {
	var totalWritten, chunkWritten int64
	buf := make([]byte, 32*1024)

	for {
		nread, rerr := src.Read(buf)
		if nread > 0 {
			chunkWritten = 0
			for chunkWritten < int64(nread) {
				nwrite, werr := dst.Write(buf[chunkWritten:nread])
				if werr != nil {
					return totalWritten, werr
				}
				chunkWritten += int64(nwrite)
			}
			totalWritten += chunkWritten
			onProgress(chunkWritten)
		}

		if rerr != nil {
			if rerr == io.EOF {
				return totalWritten, nil
			}
			return totalWritten, rerr
		}
	}
}
