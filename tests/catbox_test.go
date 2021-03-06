package tests

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
)

// Catbox holds information about a harnessed terrarium.
type Catbox struct {
	Name      string
	SID       string
	Port      uint16
	Stderr    io.ReadCloser
	Stdout    io.ReadCloser
	Command   *exec.Cmd
	WaitGroup *sync.WaitGroup
	ConfigDir string
	LogChan   <-chan string
}

const terrariumDir = ".."

func harnessCatbox(
	name,
	sid string,
) (*Catbox, error) {
	if err := buildCatbox(); err != nil {
		return nil, fmt.Errorf("error building terrarium: %s", err)
	}

	terrarium, err := startCatbox(name, sid)
	if err != nil {
		return nil, fmt.Errorf("error starting terrarium: %s", err)
	}

	var wg sync.WaitGroup

	logChan := make(chan string, 1024)

	wg.Add(1)
	go logReader(&wg, fmt.Sprintf("%s stderr", name), terrarium.Stderr, logChan)

	wg.Add(1)
	go logReader(&wg, fmt.Sprintf("%s stdout", name), terrarium.Stdout, logChan)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := terrarium.Command.Wait(); err != nil {
			log.Printf("terrarium exited: %s", err)
		}
	}()

	terrarium.WaitGroup = &wg
	terrarium.LogChan = logChan

	// It is important to wait for terrarium to fully start. If we don't, then
	// certain things we do in tests will not work well. For example, trying to
	// reload the conf by sending a SIGHUP will kill the process.
	startedRE := regexp.MustCompile(
		`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} terrarium started$`)

	if !waitForLog(logChan, startedRE) {
		terrarium.stop()
		return nil, fmt.Errorf("error waiting for terrarium to start")
	}

	return terrarium, nil
}

var builtCatbox bool

func buildCatbox() error {
	if builtCatbox {
		return nil
	}

	cmd := exec.Command("go", "build")
	cmd.Dir = terrariumDir

	log.Printf("Running %s in [%s]...", cmd.Args, cmd.Dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error building terrarium: %s: %s", err, output)
	}

	builtCatbox = true
	return nil
}

func startCatbox(
	name,
	sid string,
) (*Catbox, error) {
	tmpDir, err := ioutil.TempDir("", "boxcat-")
	if err != nil {
		return nil, fmt.Errorf("error retrieving a temporary directory: %s", err)
	}

	terrariumConf := filepath.Join(tmpDir, "terrarium.conf")

	listener, port, err := getRandomPort()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("error opening random port: %s", err)
	}

	terrarium, err := runCatbox(terrariumConf, listener, port, name, sid)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		_ = listener.Close()
		return nil, fmt.Errorf("error running terrarium: %s", err)
	}

	terrarium.ConfigDir = tmpDir
	return terrarium, nil
}

func getRandomPort() (net.Listener, uint16, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:")
	if err != nil {
		return nil, 0, fmt.Errorf("error opening a random port: %s", err)
	}

	addr := ln.Addr().String()
	colonIndex := strings.Index(addr, ":")
	portString := addr[colonIndex+1:]
	port, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		_ = ln.Close()
		return nil, 0, fmt.Errorf("error parsing port: %s", err)
	}

	return ln, uint16(port), nil
}

func runCatbox(
	conf string,
	ln net.Listener,
	port uint16,
	name,
	sid string,
) (*Catbox, error) {
	var extra string
	if err := writeConf(conf, name, sid, extra); err != nil {
		return nil, err
	}

	cmd := exec.Command("./terrarium",
		"-conf", conf,
		"-listen-fd", "3",
	)

	cmd.Dir = terrariumDir

	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		return nil, fmt.Errorf("error retrieving listener file: %s", err)
	}
	cmd.ExtraFiles = []*os.File{f}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("error retrieving stderr pipe: %s", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stderr.Close()
		return nil, fmt.Errorf("error retrieving stdout pipe: %s", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stderr.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("error starting: %s", err)
	}

	return &Catbox{
		Name:    name,
		Port:    port,
		Command: cmd,
		Stderr:  stderr,
		Stdout:  stdout,
	}, nil
}

func writeConf(
	filename,
	serverName,
	sid,
	extra string,
) error {
	// -1 because we pass in fd.
	buf := fmt.Sprintf(`
listen-port = %d
server-name = %s
ts6-sid = %s
connect-attempt-time = 100ms
%s
`,
		-1,
		serverName,
		sid,
		extra,
	)

	if err := ioutil.WriteFile(filename, []byte(buf), 0644); err != nil {
		return fmt.Errorf("error writing conf: %s: %s", serverName, err)
	}

	return nil
}

func logReader(
	wg *sync.WaitGroup,
	prefix string,
	r io.Reader,
	ch chan<- string,
) {
	defer wg.Done()

	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		log.Printf("%s: %s", prefix, line)

		select {
		case ch <- line:
		default:
		}
	}

	// Don't fail on scanner.Err(). We expect the process to go away at any time
	// so we can see errors like "file already closed".
}

func (c *Catbox) stop() {
	if err := c.Command.Process.Kill(); err != nil {
		log.Printf("error killing terrarium: %s", err)
	}
	c.WaitGroup.Wait()

	if err := os.RemoveAll(c.ConfigDir); err != nil {
		log.Fatalf("error cleaning up temporary directory: %s", err)
	}
}

func (c *Catbox) linkServer(other *Catbox) error {
	conf := filepath.Join(c.ConfigDir, "terrarium.conf")
	serversConf := filepath.Join(c.ConfigDir, "servers.conf")
	extra := fmt.Sprintf("servers-config = %s", serversConf)

	if err := writeConf(conf, c.Name, c.SID, extra); err != nil {
		return err
	}

	serversConfContent := fmt.Sprintf(`%s = %s,%d,%s,0`,
		other.Name, "127.0.0.1", other.Port, "testing")

	if err := ioutil.WriteFile(serversConf, []byte(serversConfContent),
		0644); err != nil {
		return fmt.Errorf("error writing server conf: %s: %s", serversConf, err)
	}

	return c.rehash()
}

func (c *Catbox) rehash() error {
	return errors.Wrap(
		c.Command.Process.Signal(syscall.SIGHUP),
		"error sending SIGHUP",
	)
}

func waitForLog(ch <-chan string, re *regexp.Regexp) bool {
	timeoutChan := time.After(10 * time.Second)

	for {
		select {
		case s := <-ch:
			if re.MatchString(s) {
				return true
			}
		case <-timeoutChan:
			return false
		}
	}
}
