package job

import (
	"bufio"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
)

// maxEventBytes bounds one line of the stream: a result with every candidate's routes stays far below it.
const maxEventBytes = 8 << 20

// WriteEvent writes ev to w as one line of JSON.
func WriteEvent(w io.Writer, ev Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// ReadResult consumes the solver's output stream until it ends. Progress events
// go to onProgress (which may be nil); the result, or the error the solver
// reported, is returned. A stream that ends without either is ErrNoResult.
func ReadResult(r io.Reader, onProgress func(Progress)) (*Result, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventBytes)
	var result *Result
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("solver stream: %w", err)
		}
		switch ev.Type {
		case EventProgress:
			if ev.Progress != nil && onProgress != nil {
				onProgress(*ev.Progress)
			}
		case EventResult:
			result = ev.Result
		case EventError:
			if ev.Error == nil {
				return nil, errors.New("solver stream: empty error event")
			}
			return nil, ev.Error
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("solver stream: %w", err)
	}
	if result == nil {
		return nil, ErrNoResult
	}
	return result, nil
}
