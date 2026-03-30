package multiplexer

import (
	"time"
)

// logChannelStats logs the current state of all channels for debugging memory issues
// This function reads values without lock - all accessed fields are either:
// 1. Thread-safe (channel len() operations)
// 2. Read-only after initialization (sizes, dialogueID)
// 3. Best-effort check (dialogueOK - may be slightly stale but acceptable for monitoring)
func (dg *dialogue) logChannelStats() {
	defer func() {
		if r := recover(); r != nil {
			// If logging panics, don't crash the monitor
			dg.log.Errorf("channel stats logging panic: %v, clientID: %d, dialogueID: %d", r, dg.cn.ClientID(), dg.dialogueID)
		}
	}()

	// Read channel lengths (len() is thread-safe, no lock needed)
	readInLen := len(dg.readInCh)
	writeOutLen := len(dg.writeOutCh)
	readOutLen := len(dg.readOutCh)
	writeInLen := len(dg.writeInCh)

	// Read sizes and dialogueID without lock - these are read-only after initialization
	// dialogueOK may be slightly stale but that's acceptable for monitoring purposes
	readInSize := dg.readInSize
	writeOutSize := dg.writeOutSize
	readOutSize := dg.readOutSize
	writeInSize := dg.writeInSize
	dialogueID := dg.dialogueID
	clientID := dg.cn.ClientID()

	select {
	case <-dg.ctx.Done():
		return
	default:
	}

	// Calculate usage percentages
	readInUsage := float64(readInLen) / float64(readInSize) * 100
	writeOutUsage := float64(writeOutLen) / float64(writeOutSize) * 100
	readOutUsage := float64(readOutLen) / float64(readOutSize) * 100
	writeInUsage := float64(writeInLen) / float64(writeInSize) * 100

	// Use WARN level if any channel is > 80% full, otherwise INFO
	if readInUsage > 80 || writeOutUsage > 80 || readOutUsage > 80 || writeInUsage > 80 {
		dg.log.Warnf("dialogue channel stats (HIGH USAGE), clientID: %d, dialogueID: %d, readInCh: %d/%d (%.1f%%), writeOutCh: %d/%d (%.1f%%), readOutCh: %d/%d (%.1f%%), writeInCh: %d/%d (%.1f%%)",
			clientID, dialogueID,
			readInLen, readInSize, readInUsage,
			writeOutLen, writeOutSize, writeOutUsage,
			readOutLen, readOutSize, readOutUsage,
			writeInLen, writeInSize, writeInUsage)
	} else {
		dg.log.Infof("dialogue channel stats, clientID: %d, dialogueID: %d, readInCh: %d/%d (%.1f%%), writeOutCh: %d/%d (%.1f%%), readOutCh: %d/%d (%.1f%%), writeInCh: %d/%d (%.1f%%)",
			clientID, dialogueID,
			readInLen, readInSize, readInUsage,
			writeOutLen, writeOutSize, writeOutUsage,
			readOutLen, readOutSize, readOutUsage,
			writeInLen, writeInSize, writeInUsage)
	}
}

// startChannelMonitor starts a goroutine that periodically logs channel statistics.
// enabled: only starts the goroutine when true; set false in production to avoid per-dialogue goroutine overhead.
// interval: logging interval (default: 30 seconds if <= 0)
// The goroutine will exit when dialogueOK becomes false (checked in logChannelStats)
func (dg *dialogue) startChannelMonitor(enabled bool, interval time.Duration) {
	if !enabled {
		return
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-dg.monitorStop:
				return
			case <-ticker.C:
				dg.logChannelStats()
			}
		}
	}()
}
