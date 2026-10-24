//go:build integration

package outbox_test

import (
	"os"
	"testing"

	"github.com/claudioed/seller-inbound/internal/testing/kafkatest"
	"github.com/claudioed/seller-inbound/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}
