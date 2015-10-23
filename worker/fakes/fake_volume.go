// This file was generated by counterfeiter
package fakes

import (
	"sync"
	"time"

	"github.com/concourse/atc/worker"
	"github.com/concourse/baggageclaim"
)

type FakeVolume struct {
	HandleStub        func() string
	handleMutex       sync.RWMutex
	handleArgsForCall []struct{}
	handleReturns     struct {
		result1 string
	}
	PathStub        func() string
	pathMutex       sync.RWMutex
	pathArgsForCall []struct{}
	pathReturns     struct {
		result1 string
	}
	SetTTLStub        func(time.Duration) error
	setTTLMutex       sync.RWMutex
	setTTLArgsForCall []struct {
		arg1 time.Duration
	}
	setTTLReturns struct {
		result1 error
	}
	SetPropertyStub        func(key string, value string) error
	setPropertyMutex       sync.RWMutex
	setPropertyArgsForCall []struct {
		key   string
		value string
	}
	setPropertyReturns struct {
		result1 error
	}
	ExpirationStub        func() (time.Duration, time.Time, error)
	expirationMutex       sync.RWMutex
	expirationArgsForCall []struct{}
	expirationReturns     struct {
		result1 time.Duration
		result2 time.Time
		result3 error
	}
	PropertiesStub        func() (baggageclaim.VolumeProperties, error)
	propertiesMutex       sync.RWMutex
	propertiesArgsForCall []struct{}
	propertiesReturns     struct {
		result1 baggageclaim.VolumeProperties
		result2 error
	}
	ReleaseStub        func(time.Duration)
	releaseMutex       sync.RWMutex
	releaseArgsForCall []struct {
		arg1 time.Duration
	}
}

func (fake *FakeVolume) Handle() string {
	fake.handleMutex.Lock()
	fake.handleArgsForCall = append(fake.handleArgsForCall, struct{}{})
	fake.handleMutex.Unlock()
	if fake.HandleStub != nil {
		return fake.HandleStub()
	} else {
		return fake.handleReturns.result1
	}
}

func (fake *FakeVolume) HandleCallCount() int {
	fake.handleMutex.RLock()
	defer fake.handleMutex.RUnlock()
	return len(fake.handleArgsForCall)
}

func (fake *FakeVolume) HandleReturns(result1 string) {
	fake.HandleStub = nil
	fake.handleReturns = struct {
		result1 string
	}{result1}
}

func (fake *FakeVolume) Path() string {
	fake.pathMutex.Lock()
	fake.pathArgsForCall = append(fake.pathArgsForCall, struct{}{})
	fake.pathMutex.Unlock()
	if fake.PathStub != nil {
		return fake.PathStub()
	} else {
		return fake.pathReturns.result1
	}
}

func (fake *FakeVolume) PathCallCount() int {
	fake.pathMutex.RLock()
	defer fake.pathMutex.RUnlock()
	return len(fake.pathArgsForCall)
}

func (fake *FakeVolume) PathReturns(result1 string) {
	fake.PathStub = nil
	fake.pathReturns = struct {
		result1 string
	}{result1}
}

func (fake *FakeVolume) SetTTL(arg1 time.Duration) error {
	fake.setTTLMutex.Lock()
	fake.setTTLArgsForCall = append(fake.setTTLArgsForCall, struct {
		arg1 time.Duration
	}{arg1})
	fake.setTTLMutex.Unlock()
	if fake.SetTTLStub != nil {
		return fake.SetTTLStub(arg1)
	} else {
		return fake.setTTLReturns.result1
	}
}

func (fake *FakeVolume) SetTTLCallCount() int {
	fake.setTTLMutex.RLock()
	defer fake.setTTLMutex.RUnlock()
	return len(fake.setTTLArgsForCall)
}

func (fake *FakeVolume) SetTTLArgsForCall(i int) time.Duration {
	fake.setTTLMutex.RLock()
	defer fake.setTTLMutex.RUnlock()
	return fake.setTTLArgsForCall[i].arg1
}

func (fake *FakeVolume) SetTTLReturns(result1 error) {
	fake.SetTTLStub = nil
	fake.setTTLReturns = struct {
		result1 error
	}{result1}
}

func (fake *FakeVolume) SetProperty(key string, value string) error {
	fake.setPropertyMutex.Lock()
	fake.setPropertyArgsForCall = append(fake.setPropertyArgsForCall, struct {
		key   string
		value string
	}{key, value})
	fake.setPropertyMutex.Unlock()
	if fake.SetPropertyStub != nil {
		return fake.SetPropertyStub(key, value)
	} else {
		return fake.setPropertyReturns.result1
	}
}

func (fake *FakeVolume) SetPropertyCallCount() int {
	fake.setPropertyMutex.RLock()
	defer fake.setPropertyMutex.RUnlock()
	return len(fake.setPropertyArgsForCall)
}

func (fake *FakeVolume) SetPropertyArgsForCall(i int) (string, string) {
	fake.setPropertyMutex.RLock()
	defer fake.setPropertyMutex.RUnlock()
	return fake.setPropertyArgsForCall[i].key, fake.setPropertyArgsForCall[i].value
}

func (fake *FakeVolume) SetPropertyReturns(result1 error) {
	fake.SetPropertyStub = nil
	fake.setPropertyReturns = struct {
		result1 error
	}{result1}
}

func (fake *FakeVolume) Expiration() (time.Duration, time.Time, error) {
	fake.expirationMutex.Lock()
	fake.expirationArgsForCall = append(fake.expirationArgsForCall, struct{}{})
	fake.expirationMutex.Unlock()
	if fake.ExpirationStub != nil {
		return fake.ExpirationStub()
	} else {
		return fake.expirationReturns.result1, fake.expirationReturns.result2, fake.expirationReturns.result3
	}
}

func (fake *FakeVolume) ExpirationCallCount() int {
	fake.expirationMutex.RLock()
	defer fake.expirationMutex.RUnlock()
	return len(fake.expirationArgsForCall)
}

func (fake *FakeVolume) ExpirationReturns(result1 time.Duration, result2 time.Time, result3 error) {
	fake.ExpirationStub = nil
	fake.expirationReturns = struct {
		result1 time.Duration
		result2 time.Time
		result3 error
	}{result1, result2, result3}
}

func (fake *FakeVolume) Properties() (baggageclaim.VolumeProperties, error) {
	fake.propertiesMutex.Lock()
	fake.propertiesArgsForCall = append(fake.propertiesArgsForCall, struct{}{})
	fake.propertiesMutex.Unlock()
	if fake.PropertiesStub != nil {
		return fake.PropertiesStub()
	} else {
		return fake.propertiesReturns.result1, fake.propertiesReturns.result2
	}
}

func (fake *FakeVolume) PropertiesCallCount() int {
	fake.propertiesMutex.RLock()
	defer fake.propertiesMutex.RUnlock()
	return len(fake.propertiesArgsForCall)
}

func (fake *FakeVolume) PropertiesReturns(result1 baggageclaim.VolumeProperties, result2 error) {
	fake.PropertiesStub = nil
	fake.propertiesReturns = struct {
		result1 baggageclaim.VolumeProperties
		result2 error
	}{result1, result2}
}

func (fake *FakeVolume) Release(arg1 time.Duration) {
	fake.releaseMutex.Lock()
	fake.releaseArgsForCall = append(fake.releaseArgsForCall, struct {
		arg1 time.Duration
	}{arg1})
	fake.releaseMutex.Unlock()
	if fake.ReleaseStub != nil {
		fake.ReleaseStub(arg1)
	}
}

func (fake *FakeVolume) ReleaseCallCount() int {
	fake.releaseMutex.RLock()
	defer fake.releaseMutex.RUnlock()
	return len(fake.releaseArgsForCall)
}

func (fake *FakeVolume) ReleaseArgsForCall(i int) time.Duration {
	fake.releaseMutex.RLock()
	defer fake.releaseMutex.RUnlock()
	return fake.releaseArgsForCall[i].arg1
}

var _ worker.Volume = new(FakeVolume)