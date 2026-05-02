package server

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type awsChunkedReader struct {
	r        *bufio.Reader
	remain   int64
	done     bool
	needCRLF bool
}

func requestObjectBody(r *http.Request) io.Reader {
	if !usesAWSChunkedEncoding(r.Header.Get("Content-Encoding")) {
		return r.Body
	}
	return &awsChunkedReader{r: bufio.NewReader(r.Body)}
}

func usesAWSChunkedEncoding(value string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "aws-chunked") {
			return true
		}
	}
	return false
}

func (r *awsChunkedReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	if r.remain == 0 {
		if r.needCRLF {
			if err := r.readCRLF(); err != nil {
				return 0, err
			}
			r.needCRLF = false
		}
		size, err := r.readChunkSize()
		if err != nil {
			return 0, err
		}
		if size == 0 {
			if err := r.discardTrailers(); err != nil {
				return 0, err
			}
			r.done = true
			return 0, io.EOF
		}
		r.remain = size
	}

	if int64(len(p)) > r.remain {
		p = p[:r.remain]
	}
	n, err := r.r.Read(p)
	if n > 0 {
		r.remain -= int64(n)
		if r.remain == 0 {
			r.needCRLF = true
		}
	}
	return n, err
}

func (r *awsChunkedReader) readChunkSize() (int64, error) {
	line, err := r.r.ReadString('\n')
	if err != nil {
		return 0, err
	}
	line = strings.TrimRight(line, "\r\n")
	if i := strings.IndexByte(line, ';'); i >= 0 {
		line = line[:i]
	}
	size, err := strconv.ParseInt(strings.TrimSpace(line), 16, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("invalid aws-chunked chunk size %q", line)
	}
	return size, nil
}

func (r *awsChunkedReader) readCRLF() error {
	cr, err := r.r.ReadByte()
	if err != nil {
		return err
	}
	lf, err := r.r.ReadByte()
	if err != nil {
		return err
	}
	if cr != '\r' || lf != '\n' {
		return fmt.Errorf("invalid aws-chunked chunk terminator")
	}
	return nil
}

func (r *awsChunkedReader) discardTrailers() error {
	for {
		line, err := r.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				return nil
			}
			return err
		}
		if strings.TrimRight(line, "\r\n") == "" {
			return nil
		}
	}
}
