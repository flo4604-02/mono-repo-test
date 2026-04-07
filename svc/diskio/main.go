package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/unkeyed/mono-repo-test/pkg/shared"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3458"
	}

	scratchDir := os.Getenv("UNKEY_EPHEMERAL_DISK_PATH")
	if scratchDir == "" {
		scratchDir = "/data"
	}

	var ready atomic.Bool
	startupDelay := 2 * time.Second
	log.Printf("diskio: starting up, will be ready in %s", startupDelay)
	go func() {
		time.Sleep(startupDelay)
		ready.Store(true)
		log.Println("diskio: ready to serve traffic")
	}()

	var inflight atomic.Int64

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	go func() {
		s := <-sig
		log.Printf("diskio: received %s — starting graceful shutdown", s)
		deadline := time.After(10 * time.Second)
		for inflight.Load() > 0 {
			select {
			case <-deadline:
				log.Printf("diskio: shutdown deadline reached with %d in-flight requests", inflight.Load())
				os.Exit(1)
			default:
				time.Sleep(100 * time.Millisecond)
			}
		}
		log.Printf("diskio: clean shutdown after %s", s)
		os.Exit(0)
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		defer inflight.Add(-1)

		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "diskio",
			Status:  "ok",
			Port:    port,
			Message: fmt.Sprintf("ephemeral disk test service — scratch dir: %s", scratchDir),
		})
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			shared.JSON(w, http.StatusServiceUnavailable, shared.Response{
				Service: "diskio",
				Status:  "not_ready",
				Port:    port,
				Message: "still starting up",
			})
			return
		}
		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "diskio",
			Status:  "healthy",
			Port:    port,
		})
	})
	mux.HandleFunc("POST /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			shared.JSON(w, http.StatusServiceUnavailable, shared.Response{
				Service: "diskio",
				Status:  "not_ready",
				Port:    port,
			})
			return
		}
		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "diskio",
			Status:  "healthy",
			Port:    port,
		})
	})

	// GET /disk/info — report disk usage for the scratch directory.
	mux.HandleFunc("GET /disk/info", func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		defer inflight.Add(-1)

		var stat syscall.Statfs_t
		if err := syscall.Statfs(scratchDir, &stat); err != nil {
			shared.JSON(w, http.StatusInternalServerError, shared.Response{
				Service: "diskio",
				Status:  "error",
				Port:    port,
				Message: fmt.Sprintf("statfs %s failed: %v", scratchDir, err),
			})
			return
		}

		totalBytes := stat.Blocks * uint64(stat.Bsize)
		freeBytes := stat.Bfree * uint64(stat.Bsize)
		usedBytes := totalBytes - freeBytes

		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "diskio",
			Status:  "ok",
			Port:    port,
			Message: fmt.Sprintf("path=%s total=%dMiB used=%dMiB free=%dMiB",
				scratchDir,
				totalBytes/1024/1024,
				usedBytes/1024/1024,
				freeBytes/1024/1024,
			),
		})
	})

	// GET /disk/write?size_mb=10 — write random data to a file and verify it.
	// Simulates the ffmpeg use case: write to disk, read back, confirm integrity.
	mux.HandleFunc("GET /disk/write", func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		defer inflight.Add(-1)

		sizeMB := 10
		if s := r.URL.Query().Get("size_mb"); s != "" {
			if parsed, err := strconv.Atoi(s); err == nil && parsed > 0 && parsed <= 1024 {
				sizeMB = parsed
			}
		}

		filename := filepath.Join(scratchDir, fmt.Sprintf("test-%d.bin", time.Now().UnixNano()))
		sizeBytes := int64(sizeMB) * 1024 * 1024

		log.Printf("diskio: writing %d MiB to %s", sizeMB, filename)
		start := time.Now()

		// Write random data to file.
		f, err := os.Create(filename)
		if err != nil {
			shared.JSON(w, http.StatusInternalServerError, shared.Response{
				Service: "diskio",
				Status:  "error",
				Port:    port,
				Message: fmt.Sprintf("create failed: %v", err),
			})
			return
		}

		writeHash := sha256.New()
		writer := io.MultiWriter(f, writeHash)

		if _, err := io.CopyN(writer, rand.Reader, sizeBytes); err != nil {
			f.Close()
			os.Remove(filename)
			shared.JSON(w, http.StatusInternalServerError, shared.Response{
				Service: "diskio",
				Status:  "error",
				Port:    port,
				Message: fmt.Sprintf("write failed: %v", err),
			})
			return
		}
		f.Close()
		writeDuration := time.Since(start)
		writeChecksum := hex.EncodeToString(writeHash.Sum(nil))

		// Read back and verify checksum.
		readStart := time.Now()
		f2, err := os.Open(filename)
		if err != nil {
			os.Remove(filename)
			shared.JSON(w, http.StatusInternalServerError, shared.Response{
				Service: "diskio",
				Status:  "error",
				Port:    port,
				Message: fmt.Sprintf("open for read failed: %v", err),
			})
			return
		}

		readHash := sha256.New()
		if _, err := io.Copy(readHash, f2); err != nil {
			f2.Close()
			os.Remove(filename)
			shared.JSON(w, http.StatusInternalServerError, shared.Response{
				Service: "diskio",
				Status:  "error",
				Port:    port,
				Message: fmt.Sprintf("read failed: %v", err),
			})
			return
		}
		f2.Close()
		readDuration := time.Since(readStart)
		readChecksum := hex.EncodeToString(readHash.Sum(nil))

		// Clean up.
		os.Remove(filename)

		verified := writeChecksum == readChecksum
		status := "ok"
		if !verified {
			status = "checksum_mismatch"
		}

		writeMBps := float64(sizeMB) / writeDuration.Seconds()
		readMBps := float64(sizeMB) / readDuration.Seconds()

		log.Printf("diskio: wrote %d MiB in %s (%.1f MB/s), read in %s (%.1f MB/s), verified=%v",
			sizeMB, writeDuration, writeMBps, readDuration, readMBps, verified)

		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "diskio",
			Status:  status,
			Port:    port,
			Message: fmt.Sprintf(
				"size=%dMiB write=%s(%.1fMB/s) read=%s(%.1fMB/s) verified=%v checksum=%s",
				sizeMB, writeDuration.Round(time.Millisecond), writeMBps,
				readDuration.Round(time.Millisecond), readMBps, verified,
				writeChecksum[:16],
			),
		})
	})

	// GET /disk/fill?percent=80 — fill the scratch dir to a target percentage.
	// Useful for testing what happens when disk fills up.
	mux.HandleFunc("GET /disk/fill", func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		defer inflight.Add(-1)

		targetPercent := 80
		if s := r.URL.Query().Get("percent"); s != "" {
			if parsed, err := strconv.Atoi(s); err == nil && parsed > 0 && parsed <= 99 {
				targetPercent = parsed
			}
		}

		var stat syscall.Statfs_t
		if err := syscall.Statfs(scratchDir, &stat); err != nil {
			shared.JSON(w, http.StatusInternalServerError, shared.Response{
				Service: "diskio",
				Status:  "error",
				Port:    port,
				Message: fmt.Sprintf("statfs failed: %v", err),
			})
			return
		}

		totalBytes := stat.Blocks * uint64(stat.Bsize)
		freeBytes := stat.Bfree * uint64(stat.Bsize)
		usedBytes := totalBytes - freeBytes
		currentPercent := int(usedBytes * 100 / totalBytes)

		if currentPercent >= targetPercent {
			shared.JSON(w, http.StatusOK, shared.Response{
				Service: "diskio",
				Status:  "ok",
				Port:    port,
				Message: fmt.Sprintf("already at %d%% (target %d%%)", currentPercent, targetPercent),
			})
			return
		}

		targetUsed := totalBytes * uint64(targetPercent) / 100
		bytesToWrite := int64(targetUsed - usedBytes)

		log.Printf("diskio: filling disk from %d%% to %d%% (%d MiB to write)", currentPercent, targetPercent, bytesToWrite/1024/1024)

		filename := filepath.Join(scratchDir, fmt.Sprintf("fill-%d.bin", time.Now().UnixNano()))
		f, err := os.Create(filename)
		if err != nil {
			shared.JSON(w, http.StatusInternalServerError, shared.Response{
				Service: "diskio",
				Status:  "error",
				Port:    port,
				Message: fmt.Sprintf("create fill file failed: %v", err),
			})
			return
		}

		start := time.Now()
		written, err := io.CopyN(f, rand.Reader, bytesToWrite)
		f.Close()
		duration := time.Since(start)

		if err != nil {
			shared.JSON(w, http.StatusOK, shared.Response{
				Service: "diskio",
				Status:  "partial",
				Port:    port,
				Message: fmt.Sprintf("wrote %d MiB before error: %v (disk may be full)", written/1024/1024, err),
			})
			return
		}

		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "diskio",
			Status:  "ok",
			Port:    port,
			Message: fmt.Sprintf("filled disk to ~%d%% — wrote %d MiB in %s", targetPercent, written/1024/1024, duration.Round(time.Millisecond)),
		})
	})

	// GET /disk/clean — remove all test files from scratch dir.
	mux.HandleFunc("GET /disk/clean", func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		defer inflight.Add(-1)

		entries, err := os.ReadDir(scratchDir)
		if err != nil {
			shared.JSON(w, http.StatusInternalServerError, shared.Response{
				Service: "diskio",
				Status:  "error",
				Port:    port,
				Message: fmt.Sprintf("readdir failed: %v", err),
			})
			return
		}

		removed := 0
		for _, entry := range entries {
			if err := os.Remove(filepath.Join(scratchDir, entry.Name())); err == nil {
				removed++
			}
		}

		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "diskio",
			Status:  "ok",
			Port:    port,
			Message: fmt.Sprintf("removed %d files from %s", removed, scratchDir),
		})
	})

	log.Printf("diskio: listening on :%s (scratch dir: %s)", port, scratchDir)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
