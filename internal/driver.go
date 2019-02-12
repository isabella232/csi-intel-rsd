// Copyright 2019 Intel Corporation. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package csirsd

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/intel/csi-intel-rsd/pkg/rsd"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

const (
	// DriverName defines the name that is used in Kubernetes and the CSI
	// system for the canonical, official name of this plugin
	DriverName = "csi.rsd.intel.com"

	// DriverVersion defines current CSI Driver version
	DriverVersion = "0.0.1"
)

// Driver implements the following CSI interfaces:
//
//   csi.IdentityServer
//   csi.ControllerServer
//   csi.NodeServer
//
type Driver struct {
	endpoint string
	srv      *grpc.Server

	rsdClient *rsd.Client

	// ready defines whether the driver is ready to function. This value will
	// be used by the `Identity` service via the `Probe()` method.
	ready   bool
	readyMu sync.Mutex // protects ready
}

// NewDriver returns a CSI plugin that contains the necessary gRPC
// interfaces to interact with Kubernetes over unix domain socket
func NewDriver(ep string, rsdClient *rsd.Client) *Driver {
	return &Driver{
		endpoint:  ep,
		rsdClient: rsdClient,
	}
}

// Run starts the CSI plugin by communication over the given endpoint
func (drv *Driver) Run() error {
	u, err := url.Parse(drv.endpoint)
	if err != nil {
		return fmt.Errorf("unable to parse address: %q", err)
	}

	spath := path.Join(u.Host, filepath.FromSlash(u.Path))
	if u.Host == "" {
		spath = filepath.FromSlash(u.Path)
	}

	// CSI plugins talk only over UNIX sockets currently
	if u.Scheme != "unix" {
		return fmt.Errorf("currently only unix domain sockets are supported, have: %s", u.Scheme)
	}

	// remove the socket if it's already there. This can happen if we
	// deploy a new version and the socket was created from the old running
	// plugin.
	if _, err = os.Stat(spath); !os.IsNotExist(err) {
		log.Printf("removing socket %s", spath)
		if err = os.Remove(spath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove unix domain socket file %s, error: %s", spath, err)
		}
	}

	listener, err := net.Listen(u.Scheme, spath)
	if err != nil {
		return fmt.Errorf("failed to listen socket %s: %v", spath, err)
	}

	// log response errors
	errHandler := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			log.Fatalf("method %s failed", info.FullMethod)
		}
		return resp, err
	}

	drv.srv = grpc.NewServer(grpc.UnaryInterceptor(errHandler))
	csi.RegisterIdentityServer(drv.srv, drv)
	csi.RegisterControllerServer(drv.srv, drv)
	//csi.RegisterNodeServer(drv.srv, drv)

	drv.ready = true
	log.Printf("server started serving on %s", drv.endpoint)
	return drv.srv.Serve(listener)
}