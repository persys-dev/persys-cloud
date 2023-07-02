package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"go.etcd.io/etcd/client/v3"
	"time"
)

type Driver struct {
	client *clientv3.Client
}

func NewEtcdDriver(endpoints []string, dialTimeout time.Duration) (*Driver, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return nil, err
	}
	return &Driver{
		client: cli,
	}, nil
}

func (e *Driver) Close() error {
	return e.client.Close()
}

func (e *Driver) Put(ctx context.Context, key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = e.client.Put(ctx, key, string(data))
	if err != nil {
		return err
	}
	return nil
}

func (e *Driver) Get(ctx context.Context, key string, result interface{}) error {
	resp, err := e.client.Get(ctx, key)
	if err != nil {
		return err
	}
	if len(resp.Kvs) == 0 {
		return fmt.Errorf("key not found")
	}
	err = json.Unmarshal(resp.Kvs[0].Value, result)
	if err != nil {
		return err
	}
	return nil
}

func (e *Driver) Delete(ctx context.Context, key string) error {
	_, err := e.client.Delete(ctx, key)
	if err != nil {
		return err
	}
	return nil
}
