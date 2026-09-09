// Package edgenode isolates the opt-in, backend-only LKLB pilot deployment.
package edgenode

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

const AdminID int64 = 1
const MaxBodyBytes int64 = 8 << 20

var database atomic.Pointer[sql.DB]

func Enabled() bool          { return os.Getenv("LKLB_EDGE_NODE") == "1" }
func SetDatabase(db *sql.DB) { database.Store(db) }
func CheckDatabase(ctx context.Context) error {
	if db := database.Load(); db != nil {
		return db.PingContext(ctx)
	}
	return errors.New("edge database unavailable")
}

func MemoryAvailable() bool {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "MemAvailable:" && f[2] == "kB" {
			n, err := strconv.ParseUint(f[1], 10, 64)
			return err == nil && n >= 250*1024
		}
	}
	return false
}
