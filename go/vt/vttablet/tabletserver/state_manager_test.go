/*
Copyright 2020 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tabletserver

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"vitess.io/vitess/go/sync2"
	"vitess.io/vitess/go/vt/log"
	querypb "vitess.io/vitess/go/vt/proto/query"
	topodatapb "vitess.io/vitess/go/vt/proto/topodata"
	"vitess.io/vitess/go/vt/vttablet/tabletserver/tabletenv"
)

var testNow = time.Now()

func TestStateManagerStateByName(t *testing.T) {
	sm := &stateManager{}

	sm.replHealthy = true
	sm.wantState = StateServing
	sm.state = StateNotConnected
	assert.Equal(t, "NOT_SERVING", sm.IsServingString())

	sm.state = StateNotServing
	assert.Equal(t, "NOT_SERVING", sm.IsServingString())

	sm.state = StateServing
	assert.Equal(t, "SERVING", sm.IsServingString())

	sm.wantState = StateNotServing
	assert.Equal(t, "NOT_SERVING", sm.IsServingString())
	sm.wantState = StateServing

	sm.EnterLameduck()
	assert.Equal(t, "NOT_SERVING", sm.IsServingString())
	sm.ExitLameduck()

	sm.replHealthy = false
	assert.Equal(t, "NOT_SERVING", sm.IsServingString())
}

func TestStateManagerServeMaster(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	sm.EnterLameduck()
	err := sm.SetServingType(topodatapb.TabletType_MASTER, testNow, StateServing, "")
	require.NoError(t, err)

	assert.Equal(t, false, sm.lameduck)
	assert.Equal(t, testNow, sm.terTimestamp)

	verifySubcomponent(t, 1, sm.watcher, testStateClosed)

	verifySubcomponent(t, 2, sm.se, testStateOpen)
	verifySubcomponent(t, 3, sm.vstreamer, testStateOpen)
	verifySubcomponent(t, 4, sm.qe, testStateOpen)
	verifySubcomponent(t, 5, sm.txThrottler, testStateOpen)
	verifySubcomponent(t, 6, sm.rt, testStateMaster)
	verifySubcomponent(t, 7, sm.tracker, testStateOpen)
	verifySubcomponent(t, 8, sm.te, testStateMaster)
	verifySubcomponent(t, 9, sm.messager, testStateOpen)
	verifySubcomponent(t, 10, sm.throttler, testStateOpen)

	assert.False(t, sm.se.(*testSchemaEngine).nonMaster)
	assert.True(t, sm.se.(*testSchemaEngine).ensureCalled)
	assert.False(t, sm.qe.(*testQueryEngine).stopServing)

	assert.Equal(t, topodatapb.TabletType_MASTER, sm.target.TabletType)
	assert.Equal(t, StateServing, sm.state)
}

func TestStateManagerServeNonMaster(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	err := sm.SetServingType(topodatapb.TabletType_REPLICA, testNow, StateServing, "")
	require.NoError(t, err)

	verifySubcomponent(t, 1, sm.throttler, testStateClosed)
	verifySubcomponent(t, 2, sm.messager, testStateClosed)
	verifySubcomponent(t, 3, sm.tracker, testStateClosed)
	assert.True(t, sm.se.(*testSchemaEngine).nonMaster)

	verifySubcomponent(t, 4, sm.se, testStateOpen)
	verifySubcomponent(t, 5, sm.vstreamer, testStateOpen)
	verifySubcomponent(t, 6, sm.qe, testStateOpen)
	verifySubcomponent(t, 7, sm.txThrottler, testStateOpen)
	verifySubcomponent(t, 8, sm.te, testStateNonMaster)
	verifySubcomponent(t, 9, sm.rt, testStateNonMaster)
	verifySubcomponent(t, 10, sm.watcher, testStateOpen)

	assert.Equal(t, topodatapb.TabletType_REPLICA, sm.target.TabletType)
	assert.Equal(t, StateServing, sm.state)
}

func TestStateManagerUnserveMaster(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	err := sm.SetServingType(topodatapb.TabletType_MASTER, testNow, StateNotServing, "")
	require.NoError(t, err)

	verifySubcomponent(t, 1, sm.throttler, testStateClosed)
	verifySubcomponent(t, 2, sm.messager, testStateClosed)
	verifySubcomponent(t, 3, sm.te, testStateClosed)
	assert.True(t, sm.qe.(*testQueryEngine).stopServing)

	verifySubcomponent(t, 4, sm.tracker, testStateClosed)
	verifySubcomponent(t, 5, sm.watcher, testStateClosed)
	verifySubcomponent(t, 6, sm.se, testStateOpen)
	verifySubcomponent(t, 7, sm.vstreamer, testStateOpen)
	verifySubcomponent(t, 8, sm.qe, testStateOpen)
	verifySubcomponent(t, 9, sm.txThrottler, testStateOpen)

	verifySubcomponent(t, 10, sm.rt, testStateMaster)

	assert.Equal(t, topodatapb.TabletType_MASTER, sm.target.TabletType)
	assert.Equal(t, StateNotServing, sm.state)
}

func TestStateManagerUnserveNonmaster(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	err := sm.SetServingType(topodatapb.TabletType_RDONLY, testNow, StateNotServing, "")
	require.NoError(t, err)

	verifySubcomponent(t, 1, sm.throttler, testStateClosed)
	verifySubcomponent(t, 2, sm.messager, testStateClosed)
	verifySubcomponent(t, 3, sm.te, testStateClosed)
	assert.True(t, sm.qe.(*testQueryEngine).stopServing)

	verifySubcomponent(t, 4, sm.tracker, testStateClosed)
	assert.True(t, sm.se.(*testSchemaEngine).nonMaster)

	verifySubcomponent(t, 5, sm.se, testStateOpen)
	verifySubcomponent(t, 6, sm.vstreamer, testStateOpen)
	verifySubcomponent(t, 7, sm.qe, testStateOpen)
	verifySubcomponent(t, 8, sm.txThrottler, testStateOpen)

	verifySubcomponent(t, 9, sm.rt, testStateNonMaster)
	verifySubcomponent(t, 10, sm.watcher, testStateOpen)

	assert.Equal(t, topodatapb.TabletType_RDONLY, sm.target.TabletType)
	assert.Equal(t, StateNotServing, sm.state)
}

func TestStateManagerClose(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	err := sm.SetServingType(topodatapb.TabletType_RDONLY, testNow, StateNotConnected, "")
	require.NoError(t, err)

	verifySubcomponent(t, 1, sm.throttler, testStateClosed)
	verifySubcomponent(t, 2, sm.messager, testStateClosed)
	verifySubcomponent(t, 3, sm.te, testStateClosed)
	assert.True(t, sm.qe.(*testQueryEngine).stopServing)
	verifySubcomponent(t, 4, sm.tracker, testStateClosed)

	verifySubcomponent(t, 5, sm.txThrottler, testStateClosed)
	verifySubcomponent(t, 6, sm.qe, testStateClosed)
	verifySubcomponent(t, 7, sm.watcher, testStateClosed)
	verifySubcomponent(t, 8, sm.vstreamer, testStateClosed)
	verifySubcomponent(t, 9, sm.rt, testStateClosed)
	verifySubcomponent(t, 10, sm.se, testStateClosed)

	assert.Equal(t, topodatapb.TabletType_RDONLY, sm.target.TabletType)
	assert.Equal(t, StateNotConnected, sm.state)
}

func TestStateManagerStopService(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	err := sm.SetServingType(topodatapb.TabletType_REPLICA, testNow, StateServing, "")
	require.NoError(t, err)

	assert.Equal(t, topodatapb.TabletType_REPLICA, sm.target.TabletType)
	assert.Equal(t, StateServing, sm.state)

	sm.StopService()
	assert.Equal(t, topodatapb.TabletType_REPLICA, sm.target.TabletType)
	assert.Equal(t, StateNotConnected, sm.state)
}

func TestStateManagerGracePeriod(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	sm.transitionGracePeriod = 10 * time.Millisecond

	alsoAllow := func() topodatapb.TabletType {
		sm.mu.Lock()
		defer sm.mu.Unlock()
		if len(sm.alsoAllow) == 0 {
			return topodatapb.TabletType_UNKNOWN
		}
		return sm.alsoAllow[0]
	}

	err := sm.SetServingType(topodatapb.TabletType_REPLICA, testNow, StateServing, "")
	require.NoError(t, err)

	assert.Equal(t, topodatapb.TabletType_UNKNOWN, alsoAllow())
	assert.Equal(t, topodatapb.TabletType_REPLICA, sm.target.TabletType)
	assert.Equal(t, StateServing, sm.state)

	err = sm.SetServingType(topodatapb.TabletType_MASTER, testNow, StateServing, "")
	require.NoError(t, err)

	assert.Equal(t, topodatapb.TabletType_REPLICA, alsoAllow())
	assert.Equal(t, topodatapb.TabletType_MASTER, sm.target.TabletType)
	assert.Equal(t, StateServing, sm.state)

	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, topodatapb.TabletType_UNKNOWN, alsoAllow())
}

// testWatcher is used as a hook to invoke another transition
type testWatcher struct {
	t  *testing.T
	sm *stateManager
	wg sync.WaitGroup
}

func (te *testWatcher) Open() {
}

func (te *testWatcher) Close() {
	te.wg.Add(1)
	go func() {
		defer te.wg.Done()

		err := te.sm.SetServingType(topodatapb.TabletType_RDONLY, testNow, StateNotServing, "")
		assert.NoError(te.t, err)
	}()
}

func TestStateManagerSetServingTypeRace(t *testing.T) {
	// We don't call StopService because that in turn
	// will call Close again on testWatcher.
	sm := newTestStateManager(t)
	te := &testWatcher{
		t:  t,
		sm: sm,
	}
	sm.watcher = te
	err := sm.SetServingType(topodatapb.TabletType_MASTER, testNow, StateServing, "")
	require.NoError(t, err)

	// Ensure the next call waits and then succeeds.
	te.wg.Wait()

	// End state should be the final desired state.
	assert.Equal(t, topodatapb.TabletType_RDONLY, sm.target.TabletType)
	assert.Equal(t, StateNotServing, sm.state)
}

func TestStateManagerSetServingTypeNoChange(t *testing.T) {
	log.Infof("starting")
	sm := newTestStateManager(t)
	defer sm.StopService()
	err := sm.SetServingType(topodatapb.TabletType_REPLICA, testNow, StateServing, "")
	require.NoError(t, err)

	err = sm.SetServingType(topodatapb.TabletType_REPLICA, testNow, StateServing, "")
	require.NoError(t, err)

	verifySubcomponent(t, 1, sm.throttler, testStateClosed)
	verifySubcomponent(t, 2, sm.messager, testStateClosed)
	verifySubcomponent(t, 3, sm.tracker, testStateClosed)
	assert.True(t, sm.se.(*testSchemaEngine).nonMaster)

	verifySubcomponent(t, 4, sm.se, testStateOpen)
	verifySubcomponent(t, 5, sm.vstreamer, testStateOpen)
	verifySubcomponent(t, 6, sm.qe, testStateOpen)
	verifySubcomponent(t, 7, sm.txThrottler, testStateOpen)
	verifySubcomponent(t, 8, sm.te, testStateNonMaster)
	verifySubcomponent(t, 9, sm.rt, testStateNonMaster)
	verifySubcomponent(t, 10, sm.watcher, testStateOpen)

	assert.Equal(t, topodatapb.TabletType_REPLICA, sm.target.TabletType)
	assert.Equal(t, StateServing, sm.state)
}

func TestStateManagerTransitionFailRetry(t *testing.T) {
	defer func(saved time.Duration) { transitionRetryInterval = saved }(transitionRetryInterval)
	transitionRetryInterval = 10 * time.Millisecond

	sm := newTestStateManager(t)
	defer sm.StopService()
	sm.se.(*testSchemaEngine).failMySQL = true

	err := sm.SetServingType(topodatapb.TabletType_MASTER, testNow, StateServing, "")
	require.Error(t, err)

	// Calling retryTransition while retrying should be a no-op.
	sm.retryTransition("")

	// Steal the lock and wait long enough for the retry
	// to fail, and then release it. The retry will have
	// to keep retrying.
	sm.transitioning.Acquire()
	time.Sleep(30 * time.Millisecond)
	sm.transitioning.Release()

	for {
		sm.mu.Lock()
		retrying := sm.retrying
		sm.mu.Unlock()
		if !retrying {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, topodatapb.TabletType_MASTER, sm.Target().TabletType)
	assert.Equal(t, StateServing, sm.State())
}

func TestStateManagerNotConnectedType(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	sm.EnterLameduck()
	err := sm.SetServingType(topodatapb.TabletType_RESTORE, testNow, StateNotServing, "")
	require.NoError(t, err)

	assert.Equal(t, topodatapb.TabletType_RESTORE, sm.target.TabletType)
	assert.Equal(t, StateNotConnected, sm.state)

	err = sm.SetServingType(topodatapb.TabletType_BACKUP, testNow, StateNotServing, "")
	require.NoError(t, err)

	assert.Equal(t, topodatapb.TabletType_BACKUP, sm.target.TabletType)
	assert.Equal(t, StateNotConnected, sm.state)
}

func TestStateManagerCheckMySQL(t *testing.T) {
	defer func(saved time.Duration) { transitionRetryInterval = saved }(transitionRetryInterval)
	transitionRetryInterval = 10 * time.Millisecond

	sm := newTestStateManager(t)
	defer sm.StopService()

	err := sm.SetServingType(topodatapb.TabletType_MASTER, testNow, StateServing, "")
	require.NoError(t, err)

	sm.qe.(*testQueryEngine).failMySQL = true
	order.Set(0)
	sm.CheckMySQL()

	// Rechecking immediately should be a no-op:
	sm.CheckMySQL()

	// Wait for closeAll to get under way.
	for {
		if order.Get() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait to get out of transitioning state.
	for {
		if !sm.isTransitioning() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for retry to finish.
	for {
		sm.mu.Lock()
		retrying := sm.retrying
		sm.mu.Unlock()
		if !retrying {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, topodatapb.TabletType_MASTER, sm.Target().TabletType)
	assert.Equal(t, StateServing, sm.State())
}

func TestStateManagerValidations(t *testing.T) {
	sm := newTestStateManager(t)
	target := &querypb.Target{TabletType: topodatapb.TabletType_MASTER}
	sm.target = *target

	err := sm.StartRequest(ctx, target, false)
	assert.Contains(t, err.Error(), "operation not allowed")

	sm.replHealthy = false
	sm.state = StateServing
	sm.wantState = StateServing
	err = sm.StartRequest(ctx, target, false)
	assert.Contains(t, err.Error(), "operation not allowed")

	sm.replHealthy = true
	sm.state = StateServing
	sm.wantState = StateNotServing
	err = sm.StartRequest(ctx, target, false)
	assert.Contains(t, err.Error(), "operation not allowed")

	err = sm.StartRequest(ctx, target, true)
	assert.NoError(t, err)

	sm.wantState = StateServing
	target.Keyspace = "a"
	err = sm.StartRequest(ctx, target, false)
	assert.Contains(t, err.Error(), "invalid keyspace")
	err = sm.VerifyTarget(ctx, target)
	assert.Contains(t, err.Error(), "invalid keyspace")

	target.Keyspace = ""
	target.Shard = "a"
	err = sm.StartRequest(ctx, target, false)
	assert.Contains(t, err.Error(), "invalid shard")
	err = sm.VerifyTarget(ctx, target)
	assert.Contains(t, err.Error(), "invalid shard")

	target.Shard = ""
	target.TabletType = topodatapb.TabletType_REPLICA
	err = sm.StartRequest(ctx, target, false)
	assert.Contains(t, err.Error(), "invalid tablet type")
	err = sm.VerifyTarget(ctx, target)
	assert.Contains(t, err.Error(), "invalid tablet type")

	sm.alsoAllow = []topodatapb.TabletType{topodatapb.TabletType_REPLICA}
	err = sm.StartRequest(ctx, target, false)
	assert.NoError(t, err)
	err = sm.VerifyTarget(ctx, target)
	assert.NoError(t, err)

	err = sm.StartRequest(ctx, nil, false)
	assert.Contains(t, err.Error(), "No target")
	err = sm.VerifyTarget(ctx, nil)
	assert.Contains(t, err.Error(), "No target")

	localctx := tabletenv.LocalContext()
	err = sm.StartRequest(localctx, nil, false)
	assert.NoError(t, err)
	err = sm.VerifyTarget(localctx, nil)
	assert.NoError(t, err)
}

func TestStateManagerWaitForRequests(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	target := &querypb.Target{TabletType: topodatapb.TabletType_MASTER}
	sm.target = *target
	sm.timebombDuration = 10 * time.Second

	sm.replHealthy = true
	err := sm.SetServingType(topodatapb.TabletType_MASTER, testNow, StateServing, "")
	require.NoError(t, err)

	err = sm.StartRequest(ctx, target, false)
	require.NoError(t, err)

	// This will go into transition and wait.
	// Wait for that state.
	go sm.StopService()
	for {
		if !sm.isTransitioning() {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}

	// Verify that we're still transitioning.
	assert.True(t, sm.isTransitioning())

	sm.EndRequest()

	for {
		if sm.isTransitioning() {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}
	assert.Equal(t, StateNotConnected, sm.State())
}

func TestStateManagerNotify(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()

	blpFunc = testBlpFunc

	err := sm.SetServingType(topodatapb.TabletType_REPLICA, testNow, StateServing, "")
	require.NoError(t, err)

	ch := make(chan *querypb.StreamHealthResponse, 5)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := sm.hs.Stream(context.Background(), func(shr *querypb.StreamHealthResponse) error {
			ch <- shr
			return nil
		})
		assert.Contains(t, err.Error(), "tabletserver is shutdown")
	}()
	defer wg.Wait()

	sm.Broadcast()

	gotshr := <-ch
	// Remove things we don't care about:
	gotshr.RealtimeStats = nil
	wantshr := &querypb.StreamHealthResponse{
		Target: &querypb.Target{
			TabletType: topodatapb.TabletType_REPLICA,
		},
		Serving:     true,
		TabletAlias: &topodatapb.TabletAlias{},
	}
	sm.hcticks.Stop()
	assert.Equal(t, wantshr, gotshr)
	sm.StopService()
}

func TestRefreshReplHealthLocked(t *testing.T) {
	sm := newTestStateManager(t)
	defer sm.StopService()
	rt := sm.rt.(*testReplTracker)

	sm.target.TabletType = topodatapb.TabletType_MASTER
	sm.replHealthy = false
	lag, err := sm.refreshReplHealthLocked()
	assert.Equal(t, time.Duration(0), lag)
	assert.NoError(t, err)
	assert.True(t, sm.replHealthy)

	sm.target.TabletType = topodatapb.TabletType_REPLICA
	sm.replHealthy = false
	lag, err = sm.refreshReplHealthLocked()
	assert.Equal(t, 1*time.Second, lag)
	assert.NoError(t, err)
	assert.True(t, sm.replHealthy)

	rt.err = errors.New("err")
	sm.replHealthy = true
	lag, err = sm.refreshReplHealthLocked()
	assert.Equal(t, 1*time.Second, lag)
	assert.Error(t, err)
	assert.False(t, sm.replHealthy)

	rt.err = nil
	rt.lag = 3 * time.Hour
	sm.replHealthy = true
	lag, err = sm.refreshReplHealthLocked()
	assert.Equal(t, 3*time.Hour, lag)
	assert.NoError(t, err)
	assert.False(t, sm.replHealthy)
}

func verifySubcomponent(t *testing.T, order int64, component interface{}, state testState) {
	tos := component.(orderState)
	assert.Equal(t, order, tos.Order())
	assert.Equal(t, state, tos.State())
}

func newTestStateManager(t *testing.T) *stateManager {
	order.Set(0)
	config := tabletenv.NewDefaultConfig()
	env := tabletenv.NewEnv(config, "StateManagerTest")
	sm := &stateManager{
		hs:          newHealthStreamer(env, topodatapb.TabletAlias{}),
		se:          &testSchemaEngine{},
		rt:          &testReplTracker{lag: 1 * time.Second},
		vstreamer:   &testSubcomponent{},
		tracker:     &testSubcomponent{},
		watcher:     &testSubcomponent{},
		qe:          &testQueryEngine{},
		txThrottler: &testTxThrottler{},
		te:          &testTxEngine{},
		messager:    &testSubcomponent{},
		throttler:   &testLagThrottler{},
	}
	sm.Init(env, querypb.Target{})
	sm.hs.InitDBConfig(querypb.Target{})
	log.Infof("returning sm: %p", sm)
	return sm
}

func (sm *stateManager) isTransitioning() bool {
	if sm.transitioning.TryAcquire() {
		sm.transitioning.Release()
		return false
	}
	return true
}

var order sync2.AtomicInt64

type testState int

const (
	_ = testState(iota)
	testStateOpen
	testStateClosed
	testStateMaster
	testStateNonMaster
)

type orderState interface {
	Order() int64
	State() testState
}

type testOrderState struct {
	order int64
	state testState
}

func (tos testOrderState) Order() int64 {
	return tos.order
}

func (tos testOrderState) State() testState {
	return tos.state
}

type testSchemaEngine struct {
	testOrderState
	ensureCalled bool
	nonMaster    bool

	failMySQL bool
}

func (te *testSchemaEngine) EnsureConnectionAndDB(tabletType topodatapb.TabletType) error {
	if te.failMySQL {
		te.failMySQL = false
		return errors.New("intentional error")
	}
	te.ensureCalled = true
	return nil
}

func (te *testSchemaEngine) Open() error {
	te.order = order.Add(1)
	te.state = testStateOpen
	return nil
}

func (te *testSchemaEngine) MakeNonMaster() {
	te.nonMaster = true
}

func (te *testSchemaEngine) Close() {
	te.order = order.Add(1)
	te.state = testStateClosed
}

type testReplTracker struct {
	testOrderState
	lag time.Duration
	err error
}

func (te *testReplTracker) MakeMaster() {
	te.order = order.Add(1)
	te.state = testStateMaster
}

func (te *testReplTracker) MakeNonMaster() {
	te.order = order.Add(1)
	te.state = testStateNonMaster
}

func (te *testReplTracker) Close() {
	te.order = order.Add(1)
	te.state = testStateClosed
}

func (te *testReplTracker) Status() (time.Duration, error) {
	return te.lag, te.err
}

type testQueryEngine struct {
	testOrderState
	stopServing bool

	failMySQL bool
}

func (te *testQueryEngine) Open() error {
	te.order = order.Add(1)
	te.state = testStateOpen
	return nil
}

func (te *testQueryEngine) IsMySQLReachable() error {
	if te.failMySQL {
		te.failMySQL = false
		return errors.New("intentional error")
	}
	return nil
}

func (te *testQueryEngine) StopServing() {
	te.stopServing = true
}

func (te *testQueryEngine) Close() {
	te.order = order.Add(1)
	te.state = testStateClosed
}

type testTxEngine struct {
	testOrderState
}

func (te *testTxEngine) AcceptReadWrite() error {
	te.order = order.Add(1)
	te.state = testStateMaster
	return nil
}

func (te *testTxEngine) AcceptReadOnly() error {
	te.order = order.Add(1)
	te.state = testStateNonMaster
	return nil
}

func (te *testTxEngine) Close() {
	te.order = order.Add(1)
	te.state = testStateClosed
}

type testSubcomponent struct {
	testOrderState
}

func (te *testSubcomponent) Open() {
	te.order = order.Add(1)
	te.state = testStateOpen
}

func (te *testSubcomponent) Close() {
	te.order = order.Add(1)
	te.state = testStateClosed
}

type testTxThrottler struct {
	testOrderState
}

func (te *testTxThrottler) Open() error {
	te.order = order.Add(1)
	te.state = testStateOpen
	return nil
}

func (te *testTxThrottler) Close() {
	te.order = order.Add(1)
	te.state = testStateClosed
}

type testLagThrottler struct {
	testOrderState
}

func (te *testLagThrottler) Open() error {
	te.order = order.Add(1)
	te.state = testStateOpen
	return nil
}

func (te *testLagThrottler) Close() {
	te.order = order.Add(1)
	te.state = testStateClosed
}
