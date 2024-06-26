package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/kr/binarydist"
)

var version, genDir string

type current struct {
	Version string
	Sha256  []byte
}

func generateSha256(path string) []byte {
	h := sha256.New()
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err)
	}
	h.Write(b)
	sum := h.Sum(nil)
	return sum
	// return base64.URLEncoding.EncodeToString(sum)
}

type gzReader struct {
	z, r io.ReadCloser
}

func (g *gzReader) Read(p []byte) (int, error) {
	return g.z.Read(p)
}

func (g *gzReader) Close() error {
	g.z.Close()
	return g.r.Close()
}

func newGzReader(r io.ReadCloser) io.ReadCloser {
	var err error
	g := new(gzReader)
	g.r = r
	g.z, err = gzip.NewReader(r)
	if err != nil {
		panic(err)
	}
	return g
}

func createUpdate(path string, platform string) {
	os.MkdirAll(filepath.Join(genDir, version), 0755)

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	f, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	w.Write(f)
	w.Close() // You must close this first to flush the bytes to the buffer.
	err = os.WriteFile(filepath.Join(genDir, version, platform+".gz"), buf.Bytes(), 0755)

	processUpdate := func(file fs.DirEntry) {
		fmt.Printf("Processing %s\n", file.Name())
		if !file.IsDir() {
			fmt.Printf("%s is not a directory, skipped\n", file.Name())
			return
		}
		if file.Name() == version {
			fmt.Printf("%s is current version, skipped\n", file.Name())
			return
		}

		os.Mkdir(filepath.Join(genDir, file.Name(), version), 0755)

		fName := filepath.Join(genDir, file.Name(), platform+".gz")
		old, err := os.Open(fName)
		if err != nil {
			// Don't have an old release for this os/arch, continue on
			fmt.Printf("%s found no release for this os/arch, skipped\n", file.Name())
			return
		}

		fName = filepath.Join(genDir, version, platform+".gz")
		newF, err := os.Open(fName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Can't open %s: error: %s\n", fName, err)
			os.Exit(1)
		}

		ar := newGzReader(old)
		defer ar.Close()
		br := newGzReader(newF)
		defer br.Close()
		patch := new(bytes.Buffer)
		if err := binarydist.Diff(ar, br, patch); err != nil {
			fmt.Printf("Failed to bsdiff %s, exiting\n", file.Name())
			panic(err)
		}
		os.WriteFile(filepath.Join(genDir, file.Name(), version, platform), patch.Bytes(), 0755)
		fmt.Printf("Done with %s\n", file.Name())
	}

	files, err := os.ReadDir(genDir)
	if err != nil {
		fmt.Println(err)
	}

	// spin up parallel workers to process the files:
	numCPUs := runtime.NumCPU()
	numWorkers := numCPUs
	if numWorkers > 6 {
		numWorkers = 6
	}
	fmt.Printf("Number of CPUs: %d\n", numCPUs)
	fmt.Printf("Number of workers: %d\n", numWorkers)
	filesChan := make(chan fs.DirEntry)
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			for file := range filesChan {
				processUpdate(file)
			}
			wg.Done()
		}()
	}
	for _, file := range files {
		filesChan <- file
	}
	close(filesChan)
	wg.Wait()

	c := current{Version: version, Sha256: generateSha256(path)}

	b, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		fmt.Println("error:", err)
	}
	err = os.WriteFile(filepath.Join(genDir, platform+".json"), b, 0755)
	if err != nil {
		panic(err)
	}
}

func printUsage() {
	fmt.Println("")
	fmt.Println("Positional arguments:")
	fmt.Println("\tSingle platform: go-selfupdate myapp 1.2")
	fmt.Println("\tCross platform: go-selfupdate /tmp/mybinares/ 1.2")
}

func createBuildDir() {
	os.MkdirAll(genDir, 0755)
}

func main() {
	outputDirFlag := flag.String("o", "public", "Output directory for writing updates")

	var defaultPlatform string
	goos := os.Getenv("GOOS")
	goarch := os.Getenv("GOARCH")
	if goos != "" && goarch != "" {
		defaultPlatform = goos + "-" + goarch
	} else {
		defaultPlatform = runtime.GOOS + "-" + runtime.GOARCH
	}
	platformFlag := flag.String("platform", defaultPlatform,
		"Target platform in the form OS-ARCH. Defaults to running os/arch or the combination of the environment variables GOOS and GOARCH if both are set.")

	flag.Parse()
	if flag.NArg() < 2 {
		flag.Usage()
		printUsage()
		os.Exit(0)
	}

	platform := *platformFlag
	appPath := flag.Arg(0)
	version = flag.Arg(1)
	genDir = *outputDirFlag

	createBuildDir()

	// If dir is given create update for each file
	fi, err := os.Stat(appPath)
	if err != nil {
		panic(err)
	}

	if fi.IsDir() {
		files, err := os.ReadDir(appPath)
		if err == nil {
			for _, file := range files {
				createUpdate(filepath.Join(appPath, file.Name()), file.Name())
			}
			os.Exit(0)
		}
	}

	createUpdate(appPath, platform)
}
