package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/destel/rill"
)

// This type represents a single request to the worker
type updateUserTimestampRequest struct {
	userID  int
	ReplyTo chan error
}

// This is the queue of user IDs to update.
var updateUserTimestampQueue = make(chan updateUserTimestampRequest)

// UpdateUserTimestamp is the public API for updating the last_active_at column in the users table
func UpdateUserTimestamp(ctx context.Context, userID int) error {
	// Prepare a request to the worker.
	// A ReplyTo channel is used by the worker to send us back a result.
	req := updateUserTimestampRequest{
		userID:  userID,
		ReplyTo: make(chan error, 1),
	}

	// Send request to the worker
	select {
	case <-ctx.Done():
		return ctx.Err()
	case updateUserTimestampQueue <- req:
	}

	// Block and wait for the result
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-req.ReplyTo:
		return err
	}
}

// Worker
func updateUserTimestampWorker(batchSize int, batchTimeout time.Duration, concurrency int, dbTimeout time.Duration) {
	// Start with a stream of update requests
	requests := rill.FromChan(updateUserTimestampQueue, nil)

	// Group requests into batches with timeout
	requestBatches := rill.Batch(requests, batchSize, batchTimeout)

	// Process batches with controlled database concurrency
	_ = rill.ForEach(requestBatches, concurrency, func(batch []updateUserTimestampRequest) error {
		// Create a slice of user IDs
		ids := make([]int, len(batch))
		for i, req := range batch {
			ids[i] = req.userID
		}

		// Execute batched update
		dbCtx, cancel := context.WithTimeout(context.Background(), dbTimeout)
		defer cancel()
		err := sendQueryToDB(dbCtx,
			"UPDATE users SET last_active_at = NOW() WHERE id IN (?)",
			ids,
		)

		// Send result back to all callers in this batch
		for _, req := range batch {
			req.ReplyTo <- err
			close(req.ReplyTo)
		}
		return nil
	})
}

func main() {
	ctx := context.Background()

	// Start the worker
	go updateUserTimestampWorker(
		7,                   // Batch size
		10*time.Millisecond, // Batch timeout
		2,                   // Concurrency
		10*time.Second,      // Query timeout
	)

	// Simulate many concurrent goroutines calling UpdateUserTimestamp
	var wg sync.WaitGroup

	for i := 1; i <= 100; i++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			err := UpdateUserTimestamp(ctx, userID)
			if err != nil {
				fmt.Println("Error updating user timestamp:", err)
			}
		}(i)
	}

	wg.Wait()
}

// Simulate a database query
func sendQueryToDB(_ context.Context, query string, args ...any) error {
	for _, arg := range args {
		query = strings.Replace(query, "?", fmt.Sprint(arg), 1)
	}
	fmt.Println("Executed:", query)
	return nil
}

//https://destel.dev/blog/real-time-batching-in-go
