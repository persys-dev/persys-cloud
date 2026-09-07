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
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
		_, err = s.etcdClient.Put(ctx, key, value)
		cancel()
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
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
		resp, err := s.etcdClient.Get(ctx, key, opts...)
		cancel()
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

// RetryableEtcdCASPut writes value to key only if the key's current
// ModRevision still equals expectedModRevision — i.e. nothing else has
// written to it since the caller read it. ok=false (with err=nil) means the
// compare-and-swap lost the race; the caller should re-read and retry its
// read-modify-write rather than assume the write landed.
func (s *Scheduler) RetryableEtcdCASPut(key, value string, expectedModRevision int64) (ok bool, err error) {
	if err := s.requireWritable(); err != nil {
		return false, err
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
		txnResp, txnErr := s.etcdClient.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(key), "=", expectedModRevision)).
			Then(clientv3.OpPut(key, value)).
			Commit()
		cancel()
		if txnErr == nil {
			return txnResp.Succeeded, nil
		}
		lastErr = txnErr
		etcdLogger.WithError(txnErr).WithFields(logrus.Fields{
			"attempt": attempt + 1,
			"key":     key,
		}).Warn("etcd CAS put attempt failed")
		if attempt < maxRetries {
			time.Sleep(retryWaitTime)
		}
	}
	s.enterDegraded(fmt.Sprintf("etcd CAS write failure key=%s: %v", key, lastErr))
	return false, fmt.Errorf("failed CAS put key %s after %d attempts: %v", key, maxRetries+1, lastErr)
}

// RetryableEtcdDelete performs a delete operation with retries.
func (s *Scheduler) RetryableEtcdDelete(key string, opts ...clientv3.OpOption) error {
	if err := s.requireWritable(); err != nil {
		return err
	}
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
		_, err = s.etcdClient.Delete(ctx, key, opts...)
		cancel()
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
