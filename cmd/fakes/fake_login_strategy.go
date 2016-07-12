// This file was generated by counterfeiter
package fakes

import (
	"sync"

	"github.com/cloudfoundry/bosh-init/cmd"
)

type FakeLoginStrategy struct {
	TryStub        func() error
	tryMutex       sync.RWMutex
	tryArgsForCall []struct{}
	tryReturns     struct {
		result1 error
	}
}

func (fake *FakeLoginStrategy) Try() error {
	fake.tryMutex.Lock()
	fake.tryArgsForCall = append(fake.tryArgsForCall, struct{}{})
	fake.tryMutex.Unlock()
	if fake.TryStub != nil {
		return fake.TryStub()
	} else {
		return fake.tryReturns.result1
	}
}

func (fake *FakeLoginStrategy) TryCallCount() int {
	fake.tryMutex.RLock()
	defer fake.tryMutex.RUnlock()
	return len(fake.tryArgsForCall)
}

func (fake *FakeLoginStrategy) TryReturns(result1 error) {
	fake.TryStub = nil
	fake.tryReturns = struct {
		result1 error
	}{result1}
}

var _ cmd.LoginStrategy = new(FakeLoginStrategy)