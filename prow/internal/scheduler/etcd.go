package scheduler

import (
	"context"
	"time"
	"fmt"
	"log"

	"go.etcd.io/etcd/client/v3"
)



// RetryableEtcdPut performs a put operation with retries.
func (s *Scheduler) RetryableEtcdPut(key, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		_, err = s.etcdClient.Put(ctx, key, value)
		if err == nil {
			return nil
		}
		log.Printf("Attempt %d failed to put key %s: %v", attempt+1, key, err)
		if attempt < maxRetries {
			time.Sleep(retryWaitTime)
		}
	}
	return fmt.Errorf("failed to put key %s after %d attempts: %v", key, maxRetries+1, err)
}

// RetryableEtcdGet performs a get operation with retries.
func (s *Scheduler) RetryableEtcdGet(key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := s.etcdClient.Get(ctx, key, opts...)
		if err == nil {
			return resp, nil
		}
		log.Printf("Attempt %d failed to get key %s: %v", attempt+1, key, err)
		if attempt < maxRetries {
			time.Sleep(retryWaitTime)
		}
	}
	return nil, fmt.Errorf("failed to get key %s after %d attempts: %v", key, maxRetries+1, err)
}
