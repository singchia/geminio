package conn

import (
	"time"
)

// logChannelStats logs the current state of all channels for debugging memory issues
// This function reads values without lock - all accessed fields are either:
// 1. Thread-safe (channel len() operations)
// 2. Read-only after initialization (sizes, clientID)
// 3. Best-effort check (connOK - may be slightly stale but acceptable for monitoring)
func (bc *baseConn) logChannelStats() {
	defer func() {
		if r := recover(); r != nil {
			// If logging panics, don't crash the monitor
			bc.log.Errorf("channel stats logging panic: %v, clientID: %d", r, bc.clientID)
		}
	}()

	// Read channel lengths (len() is thread-safe, no lock needed)
	readInLen := len(bc.readInCh)
	writeOutLen := len(bc.writeOutCh)
	readOutLen := len(bc.readOutCh)
	writeInLen := len(bc.writeInCh)
	heartbeatLen := len(bc.heartbeatCh)
	heartbeatWriteLen := len(bc.heartbeatWriteCh)

	// Read sizes and clientID without lock - these are read-only after initialization
	// connOK may be slightly stale but that's acceptable for monitoring purposes
	readInSize := bc.readInSize
	writeOutSize := bc.writeOutSize
	readOutSize := bc.readOutSize
	writeInSize := bc.writeInSize
	clientID := bc.clientID
	connOK := bc.connOK

	if !connOK {
		return
	}

	// Calculate usage percentages
	readInUsage := float64(readInLen) / float64(readInSize) * 100
	writeOutUsage := float64(writeOutLen) / float64(writeOutSize) * 100
	readOutUsage := float64(readOutLen) / float64(readOutSize) * 100
	writeInUsage := float64(writeInLen) / float64(writeInSize) * 100
	heartbeatUsage := float64(heartbeatLen) / 10.0 * 100
	heartbeatWriteUsage := float64(heartbeatWriteLen) / 10.0 * 100

	// Use WARN level if any channel is > 80% full, otherwise INFO
	if readInUsage > 80 || writeOutUsage > 80 || readOutUsage > 80 || writeInUsage > 80 || heartbeatUsage > 80 || heartbeatWriteUsage > 80 {
		bc.log.Warnf("channel stats (HIGH USAGE), clientID: %d, readInCh: %d/%d (%.1f%%), writeOutCh: %d/%d (%.1f%%), readOutCh: %d/%d (%.1f%%), writeInCh: %d/%d (%.1f%%), heartbeatCh: %d/10 (%.1f%%), heartbeatWriteCh: %d/10 (%.1f%%)",
			clientID,
			readInLen, readInSize, readInUsage,
			writeOutLen, writeOutSize, writeOutUsage,
			readOutLen, readOutSize, readOutUsage,
			writeInLen, writeInSize, writeInUsage,
			heartbeatLen, heartbeatUsage,
			heartbeatWriteLen, heartbeatWriteUsage)
	} else {
		bc.log.Infof("channel stats, clientID: %d, readInCh: %d/%d (%.1f%%), writeOutCh: %d/%d (%.1f%%), readOutCh: %d/%d (%.1f%%), writeInCh: %d/%d (%.1f%%), heartbeatCh: %d/10 (%.1f%%), heartbeatWriteCh: %d/10 (%.1f%%)",
			clientID,
			readInLen, readInSize, readInUsage,
			writeOutLen, writeOutSize, writeOutUsage,
			readOutLen, readOutSize, readOutUsage,
			writeInLen, writeInSize, writeInUsage,
			heartbeatLen, heartbeatUsage,
			heartbeatWriteLen, heartbeatWriteUsage)
	}
}

// startChannelMonitor starts a goroutine that periodically logs channel statistics.
// enabled: only starts the goroutine when true; set false in production to avoid per-conn goroutine overhead.
// interval: logging interval (default: 30 seconds if <= 0)
func (bc *baseConn) startChannelMonitor(enabled bool, interval time.Duration) {
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
			case <-bc.monitorStop:
				return
			case <-ticker.C:
				bc.logChannelStats()
			}
		}
	}()
}
