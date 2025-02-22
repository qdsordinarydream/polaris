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
	"sync"
	"time"

	apiconfig "github.com/polarismesh/specification/source/go/api/v1/config_manage"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"go.uber.org/zap"

	cachetypes "github.com/polarismesh/polaris/cache/api"
	api "github.com/polarismesh/polaris/common/api/v1"
	"github.com/polarismesh/polaris/common/eventhub"
	"github.com/polarismesh/polaris/common/model"
	"github.com/polarismesh/polaris/common/utils"
)

const (
	defaultLongPollingTimeout = 30000 * time.Millisecond
	QueueSize                 = 10240
)

var (
	notModifiedResponse = &apiconfig.ConfigClientResponse{
		Code:       utils.NewUInt32Value(uint32(apimodel.Code_DataNoChange)),
		ConfigFile: nil,
	}
)

type (
	FileReleaseCallback func(clientId string, rsp *apiconfig.ConfigClientResponse) bool

	WatchContextFactory func(clientId string) WatchContext

	WatchContext interface {
		// ClientID .
		ClientID() string
		// AppendInterest .
		AppendInterest(item *apiconfig.ClientConfigFileInfo)
		// RemoveInterest .
		RemoveInterest(item *apiconfig.ClientConfigFileInfo)
		// ShouldNotify .
		ShouldNotify(event *model.SimpleConfigFileRelease) bool
		// Reply .
		Reply(rsp *apiconfig.ConfigClientResponse)
		// Close .
		Close() error
		// ShouldExpire .
		ShouldExpire(now time.Time) bool
		// ListWatchFiles
		ListWatchFiles() []*apiconfig.ClientConfigFileInfo
		// IsOnce
		IsOnce() bool
	}
)

type LongPollWatchContext struct {
	clientId         string
	once             sync.Once
	finishTime       time.Time
	finishChan       chan *apiconfig.ConfigClientResponse
	watchConfigFiles map[string]*apiconfig.ClientConfigFileInfo
}

// IsOnce
func (c *LongPollWatchContext) IsOnce() bool {
	return true
}

func (c *LongPollWatchContext) GetNotifieResult() *apiconfig.ConfigClientResponse {
	return <-c.finishChan
}

func (c *LongPollWatchContext) GetNotifieResultWithTime(timeout time.Duration) (*apiconfig.ConfigClientResponse, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ret := <-c.finishChan:
		return ret, nil
	case <-timer.C:
		return nil, context.DeadlineExceeded
	}
}

func (c *LongPollWatchContext) ShouldExpire(now time.Time) bool {
	return true
}

// ClientID .
func (c *LongPollWatchContext) ClientID() string {
	return c.clientId
}

func (c *LongPollWatchContext) ShouldNotify(event *model.SimpleConfigFileRelease) bool {
	key := event.ActiveKey()
	watchFile, ok := c.watchConfigFiles[key]
	if !ok {
		return false
	}
	return watchFile.GetVersion().GetValue() < event.Version
}

func (c *LongPollWatchContext) ListWatchFiles() []*apiconfig.ClientConfigFileInfo {
	ret := make([]*apiconfig.ClientConfigFileInfo, 0, len(c.watchConfigFiles))
	for _, v := range c.watchConfigFiles {
		ret = append(ret, v)
	}
	return ret
}

// AppendInterest .
func (c *LongPollWatchContext) AppendInterest(item *apiconfig.ClientConfigFileInfo) {
	key := model.BuildKeyForClientConfigFileInfo(item)
	c.watchConfigFiles[key] = item
}

// RemoveInterest .
func (c *LongPollWatchContext) RemoveInterest(item *apiconfig.ClientConfigFileInfo) {
	key := model.BuildKeyForClientConfigFileInfo(item)
	delete(c.watchConfigFiles, key)
}

// Close .
func (c *LongPollWatchContext) Close() error {
	return nil
}

func (c *LongPollWatchContext) Reply(rsp *apiconfig.ConfigClientResponse) {
	c.once.Do(func() {
		c.finishChan <- rsp
		close(c.finishChan)
	})
}

// watchCenter 处理客户端订阅配置请求，监听配置文件发布事件通知客户端
type watchCenter struct {
	subCtx *eventhub.SubscribtionContext
	lock   sync.Mutex
	// clientId -> watchContext
	clients *utils.SyncMap[string, WatchContext]
	// fileId -> []clientId
	watchers *utils.SyncMap[string, *utils.SyncSet[string]]
	// fileCache
	fileCache cachetypes.ConfigFileCache
	cancel    context.CancelFunc
}

// NewWatchCenter 创建一个客户端监听配置发布的处理中心
func NewWatchCenter(fileCache cachetypes.ConfigFileCache) (*watchCenter, error) {
	ctx, cancel := context.WithCancel(context.Background())

	wc := &watchCenter{
		clients:   utils.NewSyncMap[string, WatchContext](),
		watchers:  utils.NewSyncMap[string, *utils.SyncSet[string]](),
		fileCache: fileCache,
		cancel:    cancel,
	}

	var err error
	wc.subCtx, err = eventhub.Subscribe(eventhub.ConfigFilePublishTopic, wc, eventhub.WithQueueSize(QueueSize))
	if err != nil {
		return nil, err
	}
	go wc.startHandleTimeoutRequestWorker(ctx)
	return wc, nil
}

// PreProcess do preprocess logic for event
func (wc *watchCenter) PreProcess(_ context.Context, e any) any {
	return e
}

// OnEvent event process logic
func (wc *watchCenter) OnEvent(ctx context.Context, arg any) error {
	event, ok := arg.(*eventhub.PublishConfigFileEvent)
	if !ok {
		log.Warn("[Config][Watcher] receive invalid event type")
		return nil
	}
	wc.notifyToWatchers(event.Message)
	return nil
}

func (wc *watchCenter) checkQuickResponseClient(watchCtx WatchContext) *apiconfig.ConfigClientResponse {
	watchFiles := watchCtx.ListWatchFiles()
	if len(watchFiles) == 0 {
		return api.NewConfigClientResponse0(apimodel.Code_InvalidWatchConfigFileFormat)
	}

	for _, configFile := range watchFiles {
		namespace := configFile.GetNamespace().GetValue()
		group := configFile.GetGroup().GetValue()
		fileName := configFile.GetFileName().GetValue()
		if namespace == "" || group == "" || fileName == "" {
			return api.NewConfigClientResponseWithInfo(apimodel.Code_BadRequest,
				"namespace & group & fileName can not be empty")
		}
		// 从缓存中获取最新的配置文件信息
		if release := wc.fileCache.GetActiveRelease(namespace, group, fileName); release != nil {
			if watchCtx.ShouldNotify(release.SimpleConfigFileRelease) {
				ret := &apiconfig.ClientConfigFileInfo{
					Namespace: utils.NewStringValue(namespace),
					Group:     utils.NewStringValue(group),
					FileName:  utils.NewStringValue(fileName),
					Version:   utils.NewUInt64Value(release.Version),
					Md5:       utils.NewStringValue(release.Md5),
					Name:      utils.NewStringValue(release.Name),
				}
				return api.NewConfigClientResponse(apimodel.Code_ExecuteSuccess, ret)
			}
		}
	}
	return nil
}

// GetWatchContext .
func (wc *watchCenter) GetWatchContext(clientId string) (WatchContext, bool) {
	return wc.clients.Load(clientId)
}

// DelWatchContext .
func (wc *watchCenter) DelWatchContext(clientId string) (WatchContext, bool) {
	return wc.clients.Delete(clientId)
}

// AddWatcher 新增订阅者
func (wc *watchCenter) AddWatcher(clientId string,
	watchFiles []*apiconfig.ClientConfigFileInfo, factory WatchContextFactory) WatchContext {
	watchCtx, _ := wc.clients.ComputeIfAbsent(clientId, func(k string) WatchContext {
		return factory(clientId)
	})

	for _, file := range watchFiles {
		fileKey := utils.GenFileId(file.Namespace.GetValue(), file.Group.GetValue(), file.FileName.GetValue())

		watchCtx.AppendInterest(file)
		clientIds, _ := wc.watchers.ComputeIfAbsent(fileKey, func(k string) *utils.SyncSet[string] {
			return utils.NewSyncSet[string]()
		})
		clientIds.Add(clientId)
	}
	return watchCtx
}

// RemoveAllWatcher 删除订阅者
func (wc *watchCenter) RemoveAllWatcher(clientId string) {
	oldVal, exist := wc.clients.Delete(clientId)
	if !exist {
		return
	}
	_ = oldVal.Close()
	for _, file := range oldVal.ListWatchFiles() {
		watchFileId := utils.GenFileId(file.Namespace.GetValue(), file.Group.GetValue(), file.FileName.GetValue())
		watchers, ok := wc.watchers.Load(watchFileId)
		if !ok {
			continue
		}
		watchers.Remove(clientId)
	}
}

// RemoveWatcher 删除订阅者
func (wc *watchCenter) RemoveWatcher(clientId string, watchConfigFiles []*apiconfig.ClientConfigFileInfo) {
	oldVal, exist := wc.clients.Delete(clientId)
	if exist {
		_ = oldVal.Close()
	}
	if len(watchConfigFiles) == 0 {
		return
	}

	for _, file := range watchConfigFiles {
		watchFileId := utils.GenFileId(file.Namespace.GetValue(), file.Group.GetValue(), file.FileName.GetValue())
		watchers, ok := wc.watchers.Load(watchFileId)
		if !ok {
			continue
		}
		watchers.Remove(clientId)
	}
}

func (wc *watchCenter) notifyToWatchers(publishConfigFile *model.SimpleConfigFileRelease) {
	watchFileId := utils.GenFileId(publishConfigFile.Namespace, publishConfigFile.Group, publishConfigFile.FileName)
	clientIds, ok := wc.watchers.Load(watchFileId)
	if !ok {
		return
	}

	log.Info("[Config][Watcher] received config file publish message.", zap.String("file", watchFileId))

	changeNotifyRequest := publishConfigFile.ToSpecNotifyClientRequest()
	response := api.NewConfigClientResponse(apimodel.Code_ExecuteSuccess, changeNotifyRequest)

	clientIds.Range(func(clientId string) {
		watchCtx, ok := wc.clients.Load(clientId)
		if !ok {
			log.Info("[Config][Watcher] not found client when do notify.", zap.String("clientId", clientId),
				zap.String("file", watchFileId))
			clientIds.Remove(clientId)
			return
		}

		if watchCtx.ShouldNotify(publishConfigFile) {
			watchCtx.Reply(response)
		}
		// 只能用一次，通知完就要立马清理掉这个 WatchContext
		if watchCtx.IsOnce() {
			wc.clients.Delete(clientId)
			wc.RemoveAllWatcher(watchCtx.ClientID())
		}
	})
}

func (wc *watchCenter) Close() {
	wc.cancel()
	wc.subCtx.Cancel()
}

func (wc *watchCenter) startHandleTimeoutRequestWorker(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if wc.clients.Len() == 0 {
				continue
			}
			tNow := time.Now()
			waitRemove := make([]WatchContext, 0, 32)
			wc.clients.Range(func(client string, watchCtx WatchContext) {
				if !watchCtx.ShouldExpire(tNow) {
					return
				}
				waitRemove = append(waitRemove, watchCtx)
			})

			for i := range waitRemove {
				watchCtx := waitRemove[i]
				watchCtx.Reply(notModifiedResponse)
				wc.RemoveAllWatcher(watchCtx.ClientID())
			}
		}
	}
}
