/**
 * Tencent is pleased to support the open source community by making Polaris available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package xdsserverv3

import (
	"strconv"
	"strings"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris/apiserver/xdsserverv3/resource"
	"github.com/polarismesh/polaris/common/model"
	"github.com/polarismesh/polaris/common/utils"
	"github.com/polarismesh/polaris/service"
)

// EDSBuilder .
type EDSBuilder struct {
	svr service.DiscoverServer
}

func (eds *EDSBuilder) Init(svr service.DiscoverServer) {
	eds.svr = svr
}

func (eds *EDSBuilder) Generate(option *resource.BuildOption) (interface{}, error) {
	var resources []types.Resource
	switch option.RunType {
	case resource.RunTypeGateway:
		resources = append(resources, eds.makeBoundEndpoints(option, core.TrafficDirection_OUTBOUND)...)
	case resource.RunTypeSidecar:
		// sidecar 场景，如果流量方向是 envoy -> 业务 POD，那么 endpoint 只能是 本地 127.0.0.1
		switch option.TrafficDirection {
		case core.TrafficDirection_INBOUND:
			inBoundEndpoints := eds.makeSelfEndpoint(option)
			resources = append(resources, inBoundEndpoints...)
		case core.TrafficDirection_OUTBOUND:
			outBoundEndpoints := eds.makeBoundEndpoints(option, core.TrafficDirection_OUTBOUND)
			resources = append(resources, outBoundEndpoints...)
		}
	}
	return resources, nil
}

func (eds *EDSBuilder) makeBoundEndpoints(option *resource.BuildOption,
	direction corev3.TrafficDirection) []types.Resource {

	services := option.Services
	selfServiceKey := option.SelfService
	isGateway := option.RunType == resource.RunTypeGateway

	var clusterLoads []types.Resource
	for svcKey, serviceInfo := range services {
		if isGateway && selfServiceKey.Equal(&svcKey) {
			continue
		}

		var lbEndpoints []*endpoint.LbEndpoint
		for _, instance := range serviceInfo.Instances {
			// 处于隔离状态或者权重为0的实例不进行下发
			if !resource.IsNormalEndpoint(instance) {
				continue
			}
			ep := &endpoint.LbEndpoint{
				HostIdentifier: &endpoint.LbEndpoint_Endpoint{
					Endpoint: &endpoint.Endpoint{
						Address: &core.Address{
							Address: &core.Address_SocketAddress{
								SocketAddress: &core.SocketAddress{
									Protocol: core.SocketAddress_TCP,
									Address:  instance.Host.Value,
									PortSpecifier: &core.SocketAddress_PortValue{
										PortValue: instance.Port.Value,
									},
								},
							},
						},
					},
				},
				HealthStatus:        resource.FormatEndpointHealth(instance),
				LoadBalancingWeight: utils.NewUInt32Value(instance.GetWeight().GetValue()),
				Metadata:            resource.GenEndpointMetaFromPolarisIns(instance),
			}
			lbEndpoints = append(lbEndpoints, ep)
		}

		cla := &endpoint.ClusterLoadAssignment{
			ClusterName: resource.MakeServiceName(svcKey, direction, option),
			Endpoints: []*endpoint.LocalityLbEndpoints{
				{
					LbEndpoints: lbEndpoints,
				},
			},
		}
		clusterLoads = append(clusterLoads, cla)
	}
	return clusterLoads
}

func (eds *EDSBuilder) makeSelfEndpoint(option *resource.BuildOption) []types.Resource {
	var clusterLoads []types.Resource
	var lbEndpoints []*endpoint.LbEndpoint

	selfServiceKey := option.SelfService
	var servicePorts []*model.ServicePort
	selfServiceInfo, ok := option.Services[selfServiceKey]
	if ok {
		servicePorts = selfServiceInfo.Ports
	} else {
		// sidecar 的服务没有注册，那就看下 envoy metadata 上有没有设置 sidecar_bindports 标签
		portsSlice := strings.Split(option.Client.Metadata[resource.SidecarBindPort], ",")
		if len(portsSlice) > 0 {
			for i := range portsSlice {
				ret, err := strconv.ParseUint(portsSlice[i], 10, 64)
				if err != nil {
					continue
				}
				if ret <= 65535 {
					servicePorts = append(servicePorts, &model.ServicePort{
						Port:     uint32(ret),
						Protocol: "TCP",
					})
				}
			}
		}
	}

	for _, port := range servicePorts {
		ep := &endpoint.LbEndpoint{
			HostIdentifier: &endpoint.LbEndpoint_Endpoint{
				Endpoint: &endpoint.Endpoint{
					Address: &core.Address{
						Address: &core.Address_SocketAddress{
							SocketAddress: &core.SocketAddress{
								Protocol: core.SocketAddress_TCP,
								Address:  "127.0.0.1",
								PortSpecifier: &core.SocketAddress_PortValue{
									PortValue: port.Port,
								},
							},
						},
					},
				},
			},
			LoadBalancingWeight: wrapperspb.UInt32(100),
			HealthStatus:        core.HealthStatus_HEALTHY,
		}
		lbEndpoints = append(lbEndpoints, ep)
	}
	cla := &endpoint.ClusterLoadAssignment{
		ClusterName: resource.MakeServiceName(selfServiceKey, core.TrafficDirection_INBOUND, option),
		Endpoints: []*endpoint.LocalityLbEndpoints{
			{
				LbEndpoints: lbEndpoints,
			},
		},
	}
	clusterLoads = append(clusterLoads, cla)
	return clusterLoads
}
