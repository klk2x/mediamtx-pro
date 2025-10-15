// Package recordcleaner contains the Pro recording cleaner.
package recordcleaner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
)

var timeNow = time.Now

// datePattern matches YYYYMMDD format directories
var datePattern = regexp.MustCompile(`^\d{8}$`)

// Cleaner removes expired recording folders from disk based on folder date.
type Cleaner struct {
	RecordPath string // Pro recorder root path
	PathConfs  map[string]*conf.Path
	Parent     logger.Writer

	ctx       context.Context
	ctxCancel func()

	chReloadConf chan map[string]*conf.Path
	done         chan struct{}
}

// Initialize initializes a Cleaner.
func (c *Cleaner) Initialize() {
	c.ctx, c.ctxCancel = context.WithCancel(context.Background())
	c.chReloadConf = make(chan map[string]*conf.Path)
	c.done = make(chan struct{})

	go c.run()
}

// Close closes the Cleaner.
func (c *Cleaner) Close() {
	c.ctxCancel()
	<-c.done
}

// Log implements logger.Writer.
func (c *Cleaner) Log(level logger.Level, format string, args ...interface{}) {
	c.Parent.Log(level, "[pro record cleaner] "+format, args...)
}

// ReloadPathConfs is called by core.Core.
func (c *Cleaner) ReloadPathConfs(pathConfs map[string]*conf.Path) {
	select {
	case c.chReloadConf <- pathConfs:
	case <-c.ctx.Done():
	}
}

func (c *Cleaner) run() {
	defer close(c.done)

	// Run immediately on start
	c.doRun()

	// Check every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.doRun()

		case cnf := <-c.chReloadConf:
			c.PathConfs = cnf

		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Cleaner) doRun() {
	// Check if RecordPath exists
	if c.RecordPath == "" {
		return
	}

	info, err := os.Stat(c.RecordPath)
	if err != nil || !info.IsDir() {
		return
	}

	now := timeNow()

	// Find the minimum recordClearDaysAgo across all paths
	minDaysAgo := 0
	for _, pathConf := range c.PathConfs {
		if pathConf.RecordClearDaysAgo > 0 {
			if minDaysAgo == 0 || pathConf.RecordClearDaysAgo < minDaysAgo {
				minDaysAgo = pathConf.RecordClearDaysAgo
			}
		}
	}

	// If no paths have cleanup configured, return
	if minDaysAgo == 0 {
		return
	}

	c.Log(logger.Debug, "scanning recording folders (minDaysAgo: %d)", minDaysAgo)

	// Scan RecordPath for date-named folders
	entries, err := os.ReadDir(c.RecordPath)
	if err != nil {
		c.Log(logger.Warn, "failed to read record path: %v", err)
		return
	}

	cutoffDate := now.AddDate(0, 0, -minDaysAgo)
	cutoffDateStr := cutoffDate.Format("20060102")

	deletedCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		folderName := entry.Name()

		// Check if folder name matches YYYYMMDD pattern
		if !datePattern.MatchString(folderName) {
			// Not a date folder, skip
			continue
		}

		// Compare folder date with cutoff date
		if folderName < cutoffDateStr {
			folderPath := filepath.Join(c.RecordPath, folderName)
			c.Log(logger.Info, "removing expired recording folder: %s", folderName)

			err := os.RemoveAll(folderPath)
			if err != nil {
				c.Log(logger.Warn, "failed to remove folder %s: %v", folderName, err)
			} else {
				deletedCount++
			}
		}
	}

	if deletedCount > 0 {
		c.Log(logger.Info, "removed %d expired recording folders", deletedCount)
	}
}
