package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/logging"
	"github.com/sirupsen/logrus"
	"go.etcd.io/etcd/client/v3"
)

var etcdLogger = logging.C("scheduler.etcd")

// RetryableEtcdPut performs a put operation with retries.
func (s *Scheduler) RetryableEtcdPut(key, value string) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		_, err = s.etcdClient.Put(ctx, key, value)
		if err == nil {
			return nil
		}
		etcdLogger.WithError(err).WithFields(logrus.Fields{
			"attempt": attempt + 1,
			"key":     key,
		}).Warn("etcd put attempt failed")
		if attempt < maxRetries {
			time.Sleep(retryWaitTime)
		}
	}
	s.enterDegraded(fmt.Sprintf("etcd write failure key=%s: %v", key, err))
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
		etcdLogger.WithError(err).WithFields(logrus.Fields{
			"attempt": attempt + 1,
			"key":     key,
		}).Warn("etcd get attempt failed")
		if attempt < maxRetries {
			time.Sleep(retryWaitTime)
		}
	}
	s.enterDegraded(fmt.Sprintf("etcd read failure key=%s: %v", key, err))
	return nil, fmt.Errorf("failed to get key %s after %d attempts: %v", key, maxRetries+1, err)
}

// RetryableEtcdDelete performs a delete operation with retries.
func (s *Scheduler) RetryableEtcdDelete(key string, opts ...clientv3.OpOption) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		_, err = s.etcdClient.Delete(ctx, key, opts...)
		if err == nil {
			return nil
		}
		etcdLogger.WithError(err).WithFields(logrus.Fields{
			"attempt": attempt + 1,
			"key":     key,
		}).Warn("etcd delete attempt failed")
		if attempt < maxRetries {
			time.Sleep(retryWaitTime)
		}
	}
	s.enterDegraded(fmt.Sprintf("etcd delete failure key=%s: %v", key, err))
	return fmt.Errorf("failed to delete key %s after %d attempts: %v", key, maxRetries+1, err)
}
