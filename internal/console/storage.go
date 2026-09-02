package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (h *handler) storagePicker(w http.ResponseWriter, _ *http.Request) {
	path, err := h.pickStorage()
	if err != nil {
		// A missing chooser is a fact about the host, not about a path the operator picked, so the remedy is safe to show and useless to hide: without it a headless node reports only that selection failed, forever.
		if errors.Is(err, errPickerUnavailable) {
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		writePrivateError(w, http.StatusBadRequest, "Storage directory selection failed.", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Path string `json:"path"`
	}{Path: path})
}

func (h *handler) storageMigrationPlan(w http.ResponseWriter, r *http.Request) {
	source, destination, err := storageMigrationPaths(r, h.cfg.StoragePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, h.migrationPlan(source, destination))
}

func storageMigrationPaths(r *http.Request, defaultSource string) (string, string, error) {
	var input struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil {
		return "", "", errors.New("a destination directory is required")
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = defaultSource
	}
	if !filepath.IsAbs(source) {
		return "", "", errors.New("source must be an absolute directory")
	}
	destination := strings.TrimSpace(input.Destination)
	if !filepath.IsAbs(destination) {
		return "", "", errors.New("destination must be an absolute directory")
	}
	return filepath.Clean(source), filepath.Clean(destination), nil
}

func (h *handler) migrationPlan(sourcePath, destinationPath string) MigrationPlan {
	source := inspectStorage(sourcePath)
	destination := inspectStorage(destinationPath)
	plan := MigrationPlan{Source: source, Destination: destination, Blockers: []string{}}
	if !source.Accessible {
		plan.Blockers = append(plan.Blockers, "the current model directory is not accessible to the agent")
	}
	if !destination.Accessible {
		plan.Blockers = append(plan.Blockers, "the destination directory is not accessible to the agent")
	}
	if filepath.Clean(source.Path) == filepath.Clean(destination.Path) {
		plan.Blockers = append(plan.Blockers, "the destination must be different from the current model directory")
	}
	if isDescendantDirectory(source.Path, destination.Path) {
		plan.Blockers = append(plan.Blockers, "the destination must not be inside the source directory")
	}
	if destination.Accessible {
		entries, err := os.ReadDir(destination.Path)
		if err != nil {
			plan.Blockers = append(plan.Blockers, "the destination directory cannot be read by the agent")
		} else if len(entries) > 0 {
			plan.Blockers = append(plan.Blockers, "choose an empty destination directory to avoid overwriting existing files")
		}
	}
	h.mu.RLock()
	downloadActive := h.pull != nil
	h.mu.RUnlock()
	if downloadActive {
		plan.Blockers = append(plan.Blockers, "wait for the current model download to finish before copying files")
	}
	plan.Ready = len(plan.Blockers) == 0
	return plan
}

func isDescendantDirectory(parent, candidate string) bool {
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return false
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedParent, resolvedCandidate)
	if err != nil || relative == "." || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (h *handler) startStorageMigration(w http.ResponseWriter, r *http.Request) {
	source, destination, err := storageMigrationPaths(r, h.cfg.StoragePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan := h.migrationPlan(source, destination)
	if !plan.Ready {
		writeJSON(w, http.StatusConflict, plan)
		return
	}

	h.mu.Lock()
	if h.pull != nil {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("wait for the current model download to finish before copying files"))
		return
	}
	if h.migration != nil && !h.migration.Done {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("a model migration is already running"))
		return
	}
	job := &migrationJob{Source: plan.Source.Path, Destination: plan.Destination.Path, Status: "copying", Total: plan.Source.UsedBytes}
	h.migration = job
	response := *job
	h.mu.Unlock()
	go h.runStorageMigration(job)
	writeJSON(w, http.StatusAccepted, response)
}

func (h *handler) migrationSnapshot() migrationJob {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.migration == nil {
		return migrationJob{Status: "idle", Done: true}
	}
	return *h.migration
}

func (h *handler) runStorageMigration(job *migrationJob) {
	err := copyStorage(job.Source, job.Destination, func(copied int64) {
		h.mu.Lock()
		if h.migration == job {
			h.migration.Completed = copied
		}
		h.mu.Unlock()
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.migration != job {
		return
	}
	job.Done = true
	if err != nil {
		job.Status, job.Error = "failed", err.Error()
		return
	}
	job.Status = "complete"
	job.Completed = job.Total
}

func copyStorage(source, destination string, report func(int64)) error {
	var copied int64
	return filepath.WalkDir(source, func(sourcePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, sourcePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		destinationPath := filepath.Join(destination, relative)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(destinationPath, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symbolic link %q", relative)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		input, err := os.Open(sourcePath)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != info.Size() {
			return fmt.Errorf("copied %d of %d bytes for %q", written, info.Size(), relative)
		}
		copied += written
		report(copied)
		return nil
	})
}

func (h *handler) storageStatus(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	cached, fresh := h.storage, time.Since(h.storageAt) < 30*time.Second
	h.mu.RUnlock()
	if fresh {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	status := inspectStorage(h.cfg.StoragePath)
	h.mu.Lock()
	h.storage, h.storageAt = status, time.Now()
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, status)
}

func inspectStorage(path string) Storage {
	status := Storage{Path: path}
	if path == "" {
		status.Error = "local model storage path is not configured"
		return status
	}
	if _, err := os.Stat(path); err != nil {
		status.Error = err.Error()
		return status
	}
	var used int64
	if err := filepath.WalkDir(path, func(entryPath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			used += info.Size()
		}
		return nil
	}); err != nil {
		status.Error = err.Error()
		return status
	}
	total, available, err := storageCapacity(path)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Accessible, status.UsedBytes, status.TotalBytes, status.AvailableBytes = true, used, total, available
	return status
}
