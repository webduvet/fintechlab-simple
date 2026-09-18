// Harness runs black-box scenarios against a running fintechlab-simple stack.
// Local:       make harness                    (after `make up` + `make up-pods`)
// Containerized: make harness-docker
// Exit code is 0 iff every registered scenario passed.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/harness"
)

func main() {
	env, err := harness.EnvFromOS()
	if err != nil {
		log.Fatalf("harness: %v", err)
	}

	timeout := 180 * time.Second
	if v := os.Getenv("HARNESS_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	scenarios := []harness.Scenario{
		harness.PaymentAPIToWebhook(),
		harness.WorldlineSettlementToSFTP(),
		harness.BankingCirclePayout(),
		harness.WorldlineConfirmationFile(),
		harness.BankingCircleSubscription(),
		harness.BankingCircleRetryDeactivation(),
		harness.B4BCompanyBoarding(),
		harness.B4BOversightGates(),
		harness.AciPaymentNotification(),
	}

	results := harness.RunAll(ctx, env, scenarios)
	os.Exit(harness.Report(results, len(scenarios)))
}
