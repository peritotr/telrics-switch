// SPDX-License-Identifier: AGPL-3.0-only

// Package ext defines the extension points the Enterprise edition plugs into.
// Core ships single-node defaults and never imports Enterprise code.
package ext

import "context"

// ClusterCoordinator shares call state between SIP nodes.
type ClusterCoordinator interface {
	// NodeID identifies this node within the cluster.
	NodeID() string
	// Peers lists the other live nodes; empty on a single node.
	Peers(ctx context.Context) ([]string, error)
}

// MediaPoolSelector picks the media relay for a new call.
type MediaPoolSelector interface {
	// Select returns the control address of the relay to use.
	Select(ctx context.Context, callID string) (string, error)
}

// LicenseProvider reports the limits the running edition allows.
type LicenseProvider interface {
	// MaxConcurrentCalls returns the call ceiling; 0 means unlimited.
	MaxConcurrentCalls() int
}

// SingleNode is the Community default for ClusterCoordinator.
type SingleNode struct{ ID string }

func (s SingleNode) NodeID() string                        { return s.ID }
func (SingleNode) Peers(context.Context) ([]string, error) { return nil, nil }

// StaticMedia is the Community default for MediaPoolSelector: one relay.
type StaticMedia struct{ Addr string }

func (s StaticMedia) Select(context.Context, string) (string, error) { return s.Addr, nil }

// CommunityLicense is the Community default for LicenseProvider.
type CommunityLicense struct{ Limit int }

func (c CommunityLicense) MaxConcurrentCalls() int { return c.Limit }
