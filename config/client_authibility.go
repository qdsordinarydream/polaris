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

package config

import (
	"context"

	apiconfig "github.com/polarismesh/specification/source/go/api/v1/config_manage"

	api "github.com/polarismesh/polaris/common/api/v1"
	"github.com/polarismesh/polaris/common/model"
	"github.com/polarismesh/polaris/common/utils"
)

// UpsertAndReleaseConfigFileFromClient 创建/更新配置文件并发布
func (s *serverAuthability) UpsertAndReleaseConfigFileFromClient(ctx context.Context,
	req *apiconfig.ConfigFilePublishInfo) *apiconfig.ConfigResponse {
	authCtx := s.collectConfigFilePublishAuthContext(ctx, []*apiconfig.ConfigFilePublishInfo{req},
		model.Modify, "UpsertAndReleaseConfigFileFromClient")
	if _, err := s.strategyMgn.GetAuthChecker().CheckClientPermission(authCtx); err != nil {
		return api.NewConfigFileResponse(convertToErrCode(err), nil)
	}

	ctx = authCtx.GetRequestContext()
	ctx = context.WithValue(ctx, utils.ContextAuthContextKey, authCtx)

	return s.targetServer.UpsertAndReleaseConfigFileFromClient(ctx, req)
}

// CreateConfigFileFromClient 调用config_file的方法创建配置文件
func (s *serverAuthability) CreateConfigFileFromClient(ctx context.Context,
	fileInfo *apiconfig.ConfigFile) *apiconfig.ConfigClientResponse {
	authCtx := s.collectClientConfigFileAuthContext(ctx,
		[]*apiconfig.ConfigFile{{
			Namespace: fileInfo.Namespace,
			Name:      fileInfo.Name,
			Group:     fileInfo.Group},
		}, model.Create, "CreateConfigFileFromClient")
	if _, err := s.strategyMgn.GetAuthChecker().CheckClientPermission(authCtx); err != nil {
		return api.NewConfigClientResponseWithInfo(convertToErrCode(err), err.Error())
	}

	ctx = authCtx.GetRequestContext()
	ctx = context.WithValue(ctx, utils.ContextAuthContextKey, authCtx)

	return s.targetServer.CreateConfigFileFromClient(ctx, fileInfo)
}

// UpdateConfigFileFromClient 调用config_file的方法更新配置文件
func (s *serverAuthability) UpdateConfigFileFromClient(ctx context.Context,
	fileInfo *apiconfig.ConfigFile) *apiconfig.ConfigClientResponse {
	authCtx := s.collectClientConfigFileAuthContext(ctx,
		[]*apiconfig.ConfigFile{fileInfo}, model.Modify, "UpdateConfigFileFromClient")
	if _, err := s.strategyMgn.GetAuthChecker().CheckClientPermission(authCtx); err != nil {
		return api.NewConfigClientResponseWithInfo(convertToErrCode(err), err.Error())
	}

	ctx = authCtx.GetRequestContext()
	ctx = context.WithValue(ctx, utils.ContextAuthContextKey, authCtx)

	return s.targetServer.UpdateConfigFileFromClient(ctx, fileInfo)
}

// DeleteConfigFileFromClient 删除配置文件，删除配置文件同时会通知客户端 Not_Found
func (s *serverAuthability) DeleteConfigFileFromClient(ctx context.Context,
	req *apiconfig.ConfigFile) *apiconfig.ConfigResponse {

	authCtx := s.collectConfigFileAuthContext(ctx,
		[]*apiconfig.ConfigFile{req}, model.Delete, "DeleteConfigFileFromClient")
	if _, err := s.strategyMgn.GetAuthChecker().CheckClientPermission(authCtx); err != nil {
		return api.NewConfigResponseWithInfo(convertToErrCode(err), err.Error())
	}

	ctx = authCtx.GetRequestContext()
	ctx = context.WithValue(ctx, utils.ContextAuthContextKey, authCtx)

	return s.targetServer.DeleteConfigFileFromClient(ctx, req)
}

// PublishConfigFileFromClient 调用config_file_release的方法发布配置文件
func (s *serverAuthability) PublishConfigFileFromClient(ctx context.Context,
	fileInfo *apiconfig.ConfigFileRelease) *apiconfig.ConfigClientResponse {
	authCtx := s.collectClientConfigFileReleaseAuthContext(ctx,
		[]*apiconfig.ConfigFileRelease{{
			Namespace: fileInfo.Namespace,
			Name:      fileInfo.FileName,
			Group:     fileInfo.Group},
		}, model.Create, "PublishConfigFileFromClient")
	if _, err := s.strategyMgn.GetAuthChecker().CheckClientPermission(authCtx); err != nil {
		return api.NewConfigClientResponseWithInfo(convertToErrCode(err), err.Error())
	}

	ctx = authCtx.GetRequestContext()
	ctx = context.WithValue(ctx, utils.ContextAuthContextKey, authCtx)

	return s.targetServer.PublishConfigFileFromClient(ctx, fileInfo)
}

// GetConfigFileForClient 从缓存中获取配置文件，如果客户端的版本号大于服务端，则服务端重新加载缓存
func (s *serverAuthability) GetConfigFileForClient(ctx context.Context,
	fileInfo *apiconfig.ClientConfigFileInfo) *apiconfig.ConfigClientResponse {
	authCtx := s.collectClientConfigFileAuthContext(ctx,
		[]*apiconfig.ConfigFile{{
			Namespace: fileInfo.Namespace,
			Name:      fileInfo.FileName,
			Group:     fileInfo.Group},
		}, model.Read, "GetConfigFileForClient")
	if _, err := s.strategyMgn.GetAuthChecker().CheckClientPermission(authCtx); err != nil {
		return api.NewConfigClientResponseWithInfo(convertToErrCode(err), err.Error())
	}

	ctx = authCtx.GetRequestContext()
	ctx = context.WithValue(ctx, utils.ContextAuthContextKey, authCtx)
	return s.targetServer.GetConfigFileForClient(ctx, fileInfo)
}

// WatchConfigFiles 监听配置文件变化
func (s *serverAuthability) LongPullWatchFile(ctx context.Context,
	request *apiconfig.ClientWatchConfigFileRequest) (WatchCallback, error) {
	authCtx := s.collectClientWatchConfigFiles(ctx, request, model.Read, "LongPullWatchFile")
	if _, err := s.strategyMgn.GetAuthChecker().CheckClientPermission(authCtx); err != nil {
		return func() *apiconfig.ConfigClientResponse {
			return api.NewConfigClientResponseWithInfo(convertToErrCode(err), err.Error())
		}, nil
	}

	ctx = authCtx.GetRequestContext()
	ctx = context.WithValue(ctx, utils.ContextAuthContextKey, authCtx)

	return s.targetServer.LongPullWatchFile(ctx, request)
}

// GetConfigFileNamesWithCache 获取某个配置分组下的配置文件
func (s *serverAuthability) GetConfigFileNamesWithCache(ctx context.Context,
	req *apiconfig.ConfigFileGroupRequest) *apiconfig.ConfigClientListResponse {

	authCtx := s.collectClientConfigFileReleaseAuthContext(ctx, []*apiconfig.ConfigFileRelease{
		{
			Namespace: req.GetConfigFileGroup().GetNamespace(),
			Group:     req.GetConfigFileGroup().GetName(),
		},
	}, model.Read, "GetConfigFileNamesWithCache")
	if _, err := s.strategyMgn.GetAuthChecker().CheckClientPermission(authCtx); err != nil {
		out := api.NewConfigClientListResponse(convertToErrCode(err))
		return out
	}

	ctx = authCtx.GetRequestContext()
	ctx = context.WithValue(ctx, utils.ContextAuthContextKey, authCtx)
	return s.targetServer.GetConfigFileNamesWithCache(ctx, req)
}
