/*
Open Source Initiative OSI - The MIT License (MIT):Licensing

The MIT License (MIT)
Copyright (c) 2013 DutchCoders <http://github.com/dutchcoders/>

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package clamd

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	RES_OK          = "OK"
	RES_FOUND       = "FOUND"
	RES_ERROR       = "ERROR"
	RES_PARSE_ERROR = "PARSE ERROR"
)

type Clamd struct {
	address string
}

type Stats struct {
	Pools    string
	State    string
	Threads  string
	Memstats string
	Queue    string
}

type ScanResult struct {
	Raw         string
	Description string
	Path        string
	Hash        string
	Size        int
	Status      string
}

var EICAR = []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)

func (c *Clamd) newConnection() (conn *CLAMDConn, err error) {
	var u *url.URL

	if u, err = url.Parse(c.address); err != nil {
		return
	}

	switch u.Scheme {
	case "tcp":
		conn, err = newCLAMDTcpConn(u.Host)
	case "unix":
		conn, err = newCLAMDUnixConn(u.Path)
	default:
		conn, err = newCLAMDUnixConn(c.address)
	}

	return
}

func (c *Clamd) simpleCommand(command string) (chan *ScanResult, error) {
	conn, err := c.newConnection()
	if err != nil {
		return nil, err
	}

	preSendCommand := time.Now()
	err = conn.sendCommand(command)
	postSendCommand := time.Now()
	if err != nil {
		return nil, err
	}

	preReadResponse := time.Now()
	ch, wg, err := conn.readResponse()
	postReadResponse := time.Now()

	go func() {
		wg.Wait()
		err := conn.Close()
		if err != nil {
			//goland:noinspection GoUnhandledErrorResult
			fmt.Fprintf(os.Stderr, "failed to close connection in ScanStream: %s", err)
		}

		postConnectionClose := time.Now()
		if command != "VERSION" {
			fmt.Printf("\tpreSend -> postConnectionClose: %s\n", postConnectionClose.Sub(preSendCommand))
		}
	}()

	if command != "VERSION" {
		s := fmt.Sprintf("=====[%s]:\n\tpreSend -> postRead: %f\n", command, postReadResponse.Sub(preSendCommand).Seconds())
		s += fmt.Sprintf("\tpostSend -> preSend: %f\n", postSendCommand.Sub(preSendCommand).Seconds())
		s += fmt.Sprintf("\tpostRead -> preRead: %f", postReadResponse.Sub(preReadResponse).Seconds())

		fmt.Println(s)
	}

	return ch, err
}

/*
Ping checks the daemon's state (should reply with PONG).
*/
func (c *Clamd) Ping() error {
	ch, err := c.simpleCommand("PING")
	if err != nil {
		return err
	}

	select {
	case s := <-ch:
		switch s.Raw {
		case "PONG":
			return nil
		default:
			return errors.New(fmt.Sprintf("Invalid response, got %v.", s))
		}
	}
}

/*
Version prints program and database versions
*/
func (c *Clamd) Version() (*ScanResult, error) {
	dataArrays, err := c.simpleCommand("VERSION")
	return <-dataArrays, err
}

// Stats provides clamd statistics about the scan queue, contents of scan
// queue, and memory usage. The exact reply format is subject to changes in future
// releases.
func (c *Clamd) Stats() (*Stats, error) {
	ch, err := c.simpleCommand("STATS")
	if err != nil {
		return nil, err
	}

	stats := &Stats{}

	for s := range ch {
		if strings.HasPrefix(s.Raw, "POOLS") {
			stats.Pools = strings.Trim(s.Raw[6:], " ")
		} else if strings.HasPrefix(s.Raw, "STATE") {
			stats.State = s.Raw
		} else if strings.HasPrefix(s.Raw, "THREADS") {
			stats.Threads = s.Raw
		} else if strings.HasPrefix(s.Raw, "QUEUE") {
			stats.Queue = s.Raw
		} else if strings.HasPrefix(s.Raw, "MEMSTATS") {
			stats.Memstats = s.Raw
		} else if strings.HasPrefix(s.Raw, "END") {
		} else {
			return nil, errors.New(fmt.Sprintf("Unknown response, got %v.", s))
		}
	}

	return stats, nil
}

/*
Reload the databases.
*/
func (c *Clamd) Reload() error {
	ch, err := c.simpleCommand("RELOAD")
	if err != nil {
		return err
	}

	select {
	case s := <-ch:
		switch s.Raw {
		case "RELOADING":
			return nil
		default:
			return errors.New(fmt.Sprintf("Invalid response, got %v.", s))
		}
	}
}

func (c *Clamd) Shutdown() error {
	_, err := c.simpleCommand("SHUTDOWN")
	if err != nil {
		return err
	}

	return err
}

/*
ScanFile scans a file or directory (recursively) with archive support enabled (a full path is
required).
*/
func (c *Clamd) ScanFile(path string) (*ScanResult, error) {
	command := fmt.Sprintf("SCAN %s", path)
	ch, err := c.simpleCommand(command)
	return <-ch, err
}

/*
RawScanFile scans a file or directory (recursively) with archive and special file support disabled
(a full path is required).
*/
func (c *Clamd) RawScanFile(path string) (*ScanResult, error) {
	command := fmt.Sprintf("RAWSCAN %s", path)
	ch, err := c.simpleCommand(command)
	return <-ch, err
}

/*
MultiScanFile scans multiple files in a standard way or scan directory (recursively) using multiple threads
(to make the scanning faster on SMP machines).
*/
func (c *Clamd) MultiScanFile(path string) (*ScanResult, error) {
	command := fmt.Sprintf("MULTISCAN %s", path)
	ch, err := c.simpleCommand(command)
	return <-ch, err
}

/*
ContScanFile scans a file or directory (recursively) with archive support enabled and don’t stop
the scanning when a virus is found.
*/
func (c *Clamd) ContScanFile(path string) (*ScanResult, error) {
	command := fmt.Sprintf("CONTSCAN %s", path)
	ch, err := c.simpleCommand(command)
	return <-ch, err
}

/*
AllMatchScanFile scans a files or directory (recursively) with archive support enabled and don’t stop
the scanning when a virus is found.
*/
func (c *Clamd) AllMatchScanFile(path string) (*ScanResult, error) {
	command := fmt.Sprintf("ALLMATCHSCAN %s", path)
	ch, err := c.simpleCommand(command)
	return <-ch, err
}

/*
ScanStream scans a stream of data. The stream is sent to clamd in chunks, after INSTREAM,
on the same socket on which the command was sent. This avoids the overhead
of establishing new TCP connections and problems with NAT. The format of the
chunk is: <length><data> where <length> is the size of the following data in
bytes expressed as a 4 byte unsigned integer in network byte order and <data> is
the actual chunk. Streaming is terminated by sending a zero-length chunk. Note:
do not exceed StreamMaxLength as defined in clamd.conf, otherwise clamd will
reply with INSTREAM size limit exceeded and close the connection
*/
func (c *Clamd) ScanStream(r io.Reader, abort chan bool) (chan *ScanResult, error) {
	id := rand.Intn(1000000)
	s := time.Now()
	sO := time.Now()
	conn, err := c.newConnection()
	if err != nil {
		return nil, err
	}
	fmt.Printf("[ScanStream(%d)] newConnection: %s\n", id, time.Now().Sub(s))
	s = time.Now()

	go func() {
		for {
			_, allowRunning := <-abort
			if !allowRunning {
				break
			}
		}
		err := conn.Close()
		if err != nil {
			//goland:noinspection GoUnhandledErrorResult
			fmt.Fprintf(os.Stderr, "failed to close connection in ScanStream: %s", err)
		}
	}()

	fmt.Printf("[ScanStream(%d)] preSendCommand(INSTREAM): %s\n", id, time.Now().Sub(s))
	s = time.Now()
	err = conn.sendCommand("INSTREAM")
	if err != nil {
		return nil, err
	}
	fmt.Printf("[ScanStream(%d)] postSendCommand(INSTREAM): %s\n", id, time.Now().Sub(s))
	s = time.Now()

	for {
		buf := make([]byte, CHUNK_SIZE)

		nr, err := r.Read(buf)
		if nr > 0 {
			err = conn.sendChunk(buf[0:nr])
			if err != nil {
				//goland:noinspection GoUnhandledErrorResult
				fmt.Fprintf(os.Stderr, "failed to write chunk to connection in ScanStream: %s", err)
			}
		}

		if err != nil {
			break
		}
	}
	fmt.Printf("[ScanStream(%d)] postFileSend(INSTREAM): %s\n", id, time.Now().Sub(s))
	s = time.Now()

	err = conn.sendEOF()
	if err != nil {
		return nil, err
	}

	fmt.Printf("[ScanStream(%d)] preReadResponse(INSTREAM): %s\n", id, time.Now().Sub(s))
	s = time.Now()
	ch, wg, err := conn.readResponse()
	fmt.Printf("[ScanStream(%d)] postReadResponse(INSTREAM): %s\n", id, time.Now().Sub(s))
	s = time.Now()

	go func() {
		s = time.Now()
		wg.Wait()
		fmt.Printf("[ScanStream(%d)] postWaitGroupWait(INSTREAM): %s\n", id, time.Now().Sub(s))
		s = time.Now()
		err := conn.Close()
		if err != nil {
			//goland:noinspection GoUnhandledErrorResult
			fmt.Fprintf(os.Stderr, "failed to close connection in ScanStream: %s", err)
		}
		fmt.Printf("[ScanStream(%d)] postConnClose(INSTREAM): %s\n", id, time.Now().Sub(s))
	}()

	fmt.Printf("[ScanStream(%d)] complete: %s\n", id, time.Now().Sub(sO))
	return ch, nil
}

func NewClamd(address string) *Clamd {
	clamd := &Clamd{address: address}
	return clamd
}
