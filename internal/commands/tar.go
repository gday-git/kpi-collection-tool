package commands

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// tarArtifactsDir creates a .tar.gz archive of the artifacts directory.
// The archive is placed next to the directory (e.g. ./kpi-collector-artifacts.tar.gz)
// and its absolute path is returned.
func tarArtifactsDir(artifactsDir string) (archivePath string, err error) {
	absDir, err := filepath.Abs(artifactsDir)
	if err != nil {
		return "", fmt.Errorf("resolve artifacts path: %w", err)
	}

	tarPath := absDir + ".tar.gz"

	outFile, err := os.Create(tarPath) //#nosec G304 -- path derived from user-controlled --artifacts-dir flag
	if err != nil {
		return "", fmt.Errorf("create archive file: %w", err)
	}
	defer func() {
		if cErr := outFile.Close(); cErr != nil && err == nil {
			err = fmt.Errorf("close archive file: %w", cErr)
		}
	}()

	gzWriter := gzip.NewWriter(outFile)
	defer func() {
		if cErr := gzWriter.Close(); cErr != nil && err == nil {
			err = fmt.Errorf("close gzip writer: %w", cErr)
		}
	}()

	tarWriter := tar.NewWriter(gzWriter)
	defer func() {
		if cErr := tarWriter.Close(); cErr != nil && err == nil {
			err = fmt.Errorf("close tar writer: %w", cErr)
		}
	}()

	baseDir := filepath.Dir(absDir)

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return addFileToTar(tarWriter, baseDir, path, info)
	})
	if err != nil {
		return "", fmt.Errorf("walk artifacts directory: %w", err)
	}

	return tarPath, nil
}

// addFileToTar writes a single file or directory entry into the tar archive.
func addFileToTar(tw *tar.Writer, baseDir, path string, info os.FileInfo) error {
	relPath, err := filepath.Rel(baseDir, path)
	if err != nil {
		return err
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = relPath

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	if info.IsDir() {
		return nil
	}

	file, err := os.Open(path) //#nosec G304 -- walking user's own artifacts directory
	if err != nil {
		return err
	}
	defer func() {
		if cErr := file.Close(); cErr != nil {
			log.Printf("warning: failed to close %s: %v", path, cErr)
		}
	}()

	_, err = io.Copy(tw, file)
	return err
}
