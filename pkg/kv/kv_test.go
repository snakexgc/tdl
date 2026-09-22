package kv

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

const (
	testCaseValid = "valid"
	testNSBar     = "bar"
	testNSFoo     = "foo"
)

func forEachStorage(t *testing.T, fn func(e Storage, t *testing.T)) {
	storages := map[Driver]string{
		DriverBolt: t.TempDir(),
		DriverFile: filepath.Join(t.TempDir(), "test.json"),
	}

	for driver, opts := range storages {
		storage, err := New(driver, opts)
		require.NoError(t, err)

		t.Run(driver.String(), func(t *testing.T) {
			fn(storage, t)
		})
		assert.NoError(t, storage.Close())
	}
}

func forEachBoltBackedStorage(t *testing.T, fn func(driver Driver, e Storage, t *testing.T)) {
	storages := map[Driver]string{
		DriverBolt: t.TempDir(),
	}

	for driver, opts := range storages {
		storage, err := New(driver, opts)
		require.NoError(t, err)

		t.Run(driver.String(), func(t *testing.T) {
			fn(driver, storage, t)
		})
		assert.NoError(t, storage.Close())
	}
}

func rawBoltValuePointer(t *testing.T, kv *boltNamespace, key string) uintptr {
	t.Helper()

	var ptr uintptr
	err := kv.db.View(func(tx *bbolt.Tx) error {
		val := tx.Bucket(kv.ns).Get([]byte(key))
		require.NotNil(t, val)
		ptr = bytePointer(val)
		return nil
	})
	require.NoError(t, err)
	require.NotZero(t, ptr)
	return ptr
}

func bytePointer(v []byte) uintptr {
	if len(v) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(v)))
}

func TestNew(t *testing.T) {
	tests := map[Driver][]struct {
		name    string
		opts    string
		wantErr bool
	}{
		DriverBolt: {
			{name: testCaseValid, opts: t.TempDir(), wantErr: false},
			{name: "invalid", opts: "", wantErr: true},
		},
		DriverFile: {
			{name: testCaseValid, opts: filepath.Join(t.TempDir(), "test.json"), wantErr: false},
		},
		Driver("unknown"): {
			{name: "unknown", opts: "", wantErr: true},
		},
	}

	for driver, tests := range tests {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%v/%s", driver, tt.name), func(t *testing.T) {
				kv, err := New(driver, tt.opts)
				if tt.wantErr {
					assert.Error(t, err)
					assert.Nil(t, kv)
				} else {
					assert.NoError(t, err)
					assert.NotNil(t, kv)
					assert.NoError(t, kv.Close())
				}
			})
		}
	}
}

func TestStorage_Open(t *testing.T) {
	forEachStorage(t, func(e Storage, t *testing.T) {
		for _, ns := range []string{testNSFoo, testNSBar, testNSFoo} {
			kv, err := e.Open(ns)
			require.NoError(t, err)
			require.NotNil(t, kv)
		}
	})
}

func TestStorage_Namespaces(t *testing.T) {
	namespaces := []string{testNSFoo, testNSBar, "baz"}

	forEachStorage(t, func(e Storage, t *testing.T) {
		for _, ns := range namespaces {
			kv, err := e.Open(ns)
			require.NoError(t, err)
			require.NotNil(t, kv)
		}

		ns, err := e.Namespaces()
		require.NoError(t, err)
		require.ElementsMatch(t, namespaces, ns)
	})
}

func TestBoltBackedStorage_GetReturnsOwnedBytes(t *testing.T) {
	forEachBoltBackedStorage(t, func(_ Driver, e Storage, t *testing.T) {
		kv, err := e.Open("foo")
		require.NoError(t, err)

		boltNamespace, ok := kv.(*boltNamespace)
		require.True(t, ok)

		value := []byte(`{"task":"watch.download.index"}`)
		require.NoError(t, boltNamespace.Set(context.TODO(), "key", value))

		rawPtr := rawBoltValuePointer(t, boltNamespace, "key")

		got, err := boltNamespace.Get(context.TODO(), "key")
		require.NoError(t, err)
		require.Equal(t, value, got)
		require.NotZero(t, bytePointer(got))
		require.NotEqual(t, rawPtr, bytePointer(got))
	})
}

func TestFileStorageConcurrentSetKeepsAllKeys(t *testing.T) {
	storage, err := New(DriverFile, filepath.Join(t.TempDir(), "test.json"))
	require.NoError(t, err)
	defer func() {
		require.NoError(t, storage.Close())
	}()

	kv, err := storage.Open("ns")
	require.NoError(t, err)

	const workers = 64
	start := make(chan struct{})
	errCh := make(chan error, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			key := fmt.Sprintf("key-%d", i)
			value := []byte(strconv.Itoa(i))
			errCh <- kv.Set(context.Background(), key, value)
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	for i := 0; i < workers; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := []byte(strconv.Itoa(i))
		got, err := kv.Get(context.Background(), key)
		require.NoError(t, err)
		require.Equal(t, value, got)
	}
}

func TestLegacyDriverIsRejected(t *testing.T) {
	_, err := New(Driver("legacy"), filepath.Join(t.TempDir(), "data.kv"))
	require.ErrorContains(t, err, "unsupported driver")
}

func TestSnapshotIsScopedAndOwnsItsBytes(t *testing.T) {
	forEachStorage(t, func(engine Storage, t *testing.T) {
		ctx := context.Background()
		first, err := engine.Open("first")
		require.NoError(t, err)
		second, err := engine.Open("second")
		require.NoError(t, err)
		require.NoError(t, first.Set(ctx, "key", []byte("first value")))
		require.NoError(t, second.Set(ctx, "secret", []byte("second value")))
		values, err := engine.Snapshot(ctx, "first")
		require.NoError(t, err)
		require.Equal(t, map[string][]byte{"key": []byte("first value")}, values)
		values["key"][0] = 'X'
		value, err := first.Get(ctx, "key")
		require.NoError(t, err)
		require.Equal(t, []byte("first value"), value)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = engine.Snapshot(canceled, "first")
		require.ErrorIs(t, err, context.Canceled)
	})
}
