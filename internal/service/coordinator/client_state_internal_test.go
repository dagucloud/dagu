// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestOrderStateCoordinatorMembersIsDeterministicAndMinimizesMovement(t *testing.T) {
	t.Parallel()

	members := []exec.HostInfo{
		{ID: "coord-b", Host: "127.0.0.1", Port: 1002},
		{ID: "coord-a", Host: "127.0.0.1", Port: 1001},
		{ID: "coord-c", Host: "127.0.0.1", Port: 1003},
	}
	reordered := []exec.HostInfo{members[2], members[0], members[1]}

	key := "dag\x00namespace"
	ordered := orderStateCoordinatorMembers(members, key)
	owner := ordered[0]
	require.Equal(t, owner.ID, orderStateCoordinatorMembers(reordered, key)[0].ID)

	remaining := make([]exec.HostInfo, 0, len(members)-1)
	removed := ordered[len(ordered)-1]
	for _, member := range members {
		if member.ID == removed.ID {
			continue
		}
		remaining = append(remaining, member)
	}
	require.Equal(t, owner.ID, orderStateCoordinatorMembers(remaining, key)[0].ID)
}

func TestPinnedStateCoordinatorDiscoveryDoesNotHoldPinLock(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	registry := &blockingStateRegistry{started: started, release: release}
	cli := &clientImpl{
		config:            DefaultConfig(),
		registry:          registry,
		clients:           make(map[string]*client),
		stateCoordinators: make(map[string]pinnedStateCoordinator),
		state:             &Metrics{IsConnected: true},
	}

	done := make(chan error, 1)
	go func() {
		_, err := cli.pinnedStateCoordinator(context.Background(), "new-keyspace")
		done <- err
	}()

	<-started
	locked := make(chan struct{})
	go func() {
		cli.stateCoordinatorMu.Lock()
		defer cli.stateCoordinatorMu.Unlock()
		close(locked)
	}()

	select {
	case <-locked:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("state coordinator pin lock was held during service discovery")
	}

	close(release)
	require.Error(t, <-done)
}

func TestRememberPinnedStateCoordinatorEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	cli := &clientImpl{stateCoordinators: make(map[string]pinnedStateCoordinator)}
	base := time.Unix(0, 0).UTC()
	for i := range maxPinnedStateCoordinators {
		key := fmt.Sprintf("key-%04d", i)
		member := exec.HostInfo{ID: fmt.Sprintf("coord-%04d", i)}
		cli.stateCoordinators[key] = pinnedStateCoordinator{
			member:    member,
			memberKey: coordinatorMemberKey(member),
			lastUsed:  base.Add(time.Duration(i) * time.Second),
		}
	}

	newMember := exec.HostInfo{ID: "coord-new"}
	cli.rememberPinnedStateCoordinatorLocked("key-new", newMember)

	require.Len(t, cli.stateCoordinators, maxPinnedStateCoordinators)
	require.NotContains(t, cli.stateCoordinators, "key-0000")
	require.Equal(t, "coord-new", cli.stateCoordinators["key-new"].member.ID)
}

type blockingStateRegistry struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (r *blockingStateRegistry) Register(context.Context, exec.ServiceName, exec.HostInfo) error {
	return nil
}

func (r *blockingStateRegistry) Unregister(context.Context) {}

func (r *blockingStateRegistry) GetServiceMembers(context.Context, exec.ServiceName) ([]exec.HostInfo, error) {
	close(r.started)
	<-r.release
	return nil, fmt.Errorf("discovery stopped")
}

func (r *blockingStateRegistry) UpdateStatus(context.Context, exec.ServiceName, exec.ServiceStatus) error {
	return nil
}
