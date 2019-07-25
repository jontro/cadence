// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package history

import (
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/uber-go/tally"
	"github.com/uber/cadence/.gen/go/shared"
	"github.com/uber/cadence/client"
	"github.com/uber/cadence/common"
	"github.com/uber/cadence/common/cache"
	"github.com/uber/cadence/common/cluster"
	"github.com/uber/cadence/common/log"
	"github.com/uber/cadence/common/log/loggerimpl"
	"github.com/uber/cadence/common/messaging"
	"github.com/uber/cadence/common/metrics"
	"github.com/uber/cadence/common/mocks"
	"github.com/uber/cadence/common/persistence"
	"github.com/uber/cadence/common/service"
)

type (
	conflictResolverSuite struct {
		suite.Suite
		logger              log.Logger
		mockExecutionMgr    *mocks.ExecutionManager
		mockHistoryMgr      *mocks.HistoryManager
		mockHistoryV2Mgr    *mocks.HistoryV2Manager
		mockShardManager    *mocks.ShardManager
		mockClusterMetadata *mocks.ClusterMetadata
		mockProducer        *mocks.KafkaProducer
		mockMetadataMgr     *mocks.MetadataManager
		mockMessagingClient messaging.Client
		mockService         service.Service
		mockShard           *shardContextImpl
		mockContext         *workflowExecutionContextImpl
		mockDomainCache     *cache.DomainCacheMock
		mockClientBean      *client.MockClientBean
		mockEventsCache     *MockEventsCache

		conflictResolver *conflictResolverImpl
	}
)

func TestConflictResolverSuite(t *testing.T) {
	s := new(conflictResolverSuite)
	suite.Run(t, s)
}

func (s *conflictResolverSuite) SetupSuite() {
}

func (s *conflictResolverSuite) TearDownSuite() {

}

func (s *conflictResolverSuite) SetupTest() {
	s.logger = loggerimpl.NewDevelopmentForTest(s.Suite)
	s.mockHistoryMgr = &mocks.HistoryManager{}
	s.mockHistoryV2Mgr = &mocks.HistoryV2Manager{}
	s.mockExecutionMgr = &mocks.ExecutionManager{}
	s.mockClusterMetadata = &mocks.ClusterMetadata{}
	s.mockShardManager = &mocks.ShardManager{}
	s.mockProducer = &mocks.KafkaProducer{}
	s.mockMessagingClient = mocks.NewMockMessagingClient(s.mockProducer, nil)
	s.mockMetadataMgr = &mocks.MetadataManager{}
	metricsClient := metrics.NewClient(tally.NoopScope, metrics.History)
	s.mockClientBean = &client.MockClientBean{}
	s.mockService = service.NewTestService(s.mockClusterMetadata, s.mockMessagingClient, metricsClient, s.mockClientBean)
	s.mockDomainCache = &cache.DomainCacheMock{}
	s.mockEventsCache = &MockEventsCache{}

	s.mockShard = &shardContextImpl{
		service:                   s.mockService,
		shardInfo:                 &persistence.ShardInfo{ShardID: 10, RangeID: 1, TransferAckLevel: 0},
		transferSequenceNumber:    1,
		executionManager:          s.mockExecutionMgr,
		shardManager:              s.mockShardManager,
		historyMgr:                s.mockHistoryMgr,
		maxTransferSequenceNumber: 100000,
		closeCh:                   make(chan int, 100),
		config:                    NewDynamicConfigForTest(),
		logger:                    s.logger,
		domainCache:               s.mockDomainCache,
		metricsClient:             metrics.NewClient(tally.NoopScope, metrics.History),
		eventsCache:               s.mockEventsCache,
	}
	s.mockContext = newWorkflowExecutionContext(validDomainID, shared.WorkflowExecution{
		WorkflowId: common.StringPtr("some random workflow ID"),
		RunId:      common.StringPtr(validRunID),
	}, s.mockShard, s.mockExecutionMgr, s.logger)
	s.mockClusterMetadata.On("GetCurrentClusterName").Return(cluster.TestCurrentClusterName)
	s.conflictResolver = newConflictResolver(s.mockShard, s.mockContext, s.mockHistoryMgr, s.mockHistoryV2Mgr, s.logger)
}

func (s *conflictResolverSuite) TearDownTest() {
	s.mockHistoryMgr.AssertExpectations(s.T())
	s.mockHistoryV2Mgr.AssertExpectations(s.T())
	s.mockExecutionMgr.AssertExpectations(s.T())
	s.mockShardManager.AssertExpectations(s.T())
	s.mockProducer.AssertExpectations(s.T())
	s.mockMetadataMgr.AssertExpectations(s.T())
	s.mockClientBean.AssertExpectations(s.T())
	s.mockDomainCache.AssertExpectations(s.T())
	s.mockEventsCache.AssertExpectations(s.T())
}

func (s *conflictResolverSuite) TestGetHistory() {
	domainID := s.mockContext.domainID
	execution := s.mockContext.workflowExecution
	nextEventID := int64(101)

	event1 := &shared.HistoryEvent{
		EventId:                                 common.Int64Ptr(1),
		WorkflowExecutionStartedEventAttributes: &shared.WorkflowExecutionStartedEventAttributes{},
	}
	event2 := &shared.HistoryEvent{
		EventId:                              common.Int64Ptr(2),
		DecisionTaskScheduledEventAttributes: &shared.DecisionTaskScheduledEventAttributes{},
	}
	event3 := &shared.HistoryEvent{
		EventId:                            common.Int64Ptr(3),
		DecisionTaskStartedEventAttributes: &shared.DecisionTaskStartedEventAttributes{},
	}
	event4 := &shared.HistoryEvent{
		EventId:                              common.Int64Ptr(4),
		DecisionTaskCompletedEventAttributes: &shared.DecisionTaskCompletedEventAttributes{},
	}
	event5 := &shared.HistoryEvent{
		EventId:                              common.Int64Ptr(5),
		ActivityTaskScheduledEventAttributes: &shared.ActivityTaskScheduledEventAttributes{},
	}

	pageToken := []byte("some random token")
	s.mockHistoryMgr.On("GetWorkflowExecutionHistory", &persistence.GetWorkflowExecutionHistoryRequest{
		DomainID:      domainID,
		Execution:     execution,
		FirstEventID:  common.FirstEventID,
		NextEventID:   nextEventID,
		PageSize:      defaultHistoryPageSize,
		NextPageToken: nil,
	}).Return(&persistence.GetWorkflowExecutionHistoryResponse{
		History:          &shared.History{Events: []*shared.HistoryEvent{event1, event2}},
		NextPageToken:    pageToken,
		LastFirstEventID: event1.GetEventId(),
	}, nil)
	history, _, firstEventID, token, err := s.conflictResolver.getHistory(domainID, execution, common.FirstEventID, nextEventID, nil, 0, nil)
	s.Nil(err)
	s.Equal(history, []*shared.HistoryEvent{event1, event2})
	s.Equal(pageToken, token)
	s.Equal(firstEventID, event1.GetEventId())

	s.mockHistoryMgr.On("GetWorkflowExecutionHistory", &persistence.GetWorkflowExecutionHistoryRequest{
		DomainID:      domainID,
		Execution:     execution,
		FirstEventID:  common.FirstEventID,
		NextEventID:   nextEventID,
		PageSize:      defaultHistoryPageSize,
		NextPageToken: pageToken,
	}).Return(&persistence.GetWorkflowExecutionHistoryResponse{
		History:          &shared.History{Events: []*shared.HistoryEvent{event3, event4, event5}},
		NextPageToken:    nil,
		LastFirstEventID: event4.GetEventId(),
	}, nil)
	history, _, firstEventID, token, err = s.conflictResolver.getHistory(domainID, execution, common.FirstEventID, nextEventID, token, 0, nil)
	s.Nil(err)
	s.Equal(history, []*shared.HistoryEvent{event3, event4, event5})
	s.Empty(token)
	s.Equal(firstEventID, event4.GetEventId())
}

func (s *conflictResolverSuite) TestReset() {
	prevRunID := uuid.New()
	sourceCluster := "some random source cluster"
	startTime := time.Now()
	domainID := s.mockContext.domainID
	execution := s.mockContext.workflowExecution
	nextEventID := int64(2)

	event1 := &shared.HistoryEvent{
		EventId: common.Int64Ptr(1),
		Version: common.Int64Ptr(12),
		WorkflowExecutionStartedEventAttributes: &shared.WorkflowExecutionStartedEventAttributes{
			WorkflowType:                        &shared.WorkflowType{Name: common.StringPtr("some random workflow type")},
			TaskList:                            &shared.TaskList{Name: common.StringPtr("some random workflow type")},
			Input:                               []byte("some random input"),
			ExecutionStartToCloseTimeoutSeconds: common.Int32Ptr(123),
			TaskStartToCloseTimeoutSeconds:      common.Int32Ptr(233),
			Identity:                            common.StringPtr("some random identity"),
		},
	}
	event2 := &shared.HistoryEvent{
		EventId:                              common.Int64Ptr(2),
		DecisionTaskScheduledEventAttributes: &shared.DecisionTaskScheduledEventAttributes{},
	}

	s.mockClusterMetadata.On("ClusterNameForFailoverVersion", event1.GetVersion()).Return(sourceCluster)
	s.mockHistoryMgr.On("GetWorkflowExecutionHistory", &persistence.GetWorkflowExecutionHistoryRequest{
		DomainID:      domainID,
		Execution:     execution,
		FirstEventID:  common.FirstEventID,
		NextEventID:   nextEventID,
		PageSize:      defaultHistoryPageSize,
		NextPageToken: nil,
	}).Return(&persistence.GetWorkflowExecutionHistoryResponse{
		History:          &shared.History{Events: []*shared.HistoryEvent{event1, event2}},
		NextPageToken:    nil,
		LastFirstEventID: event1.GetEventId(),
	}, nil)

	s.mockContext.updateCondition = int64(59)
	createRequestID := uuid.New()

	executionInfo := &persistence.WorkflowExecutionInfo{
		DomainID:                 domainID,
		WorkflowID:               execution.GetWorkflowId(),
		RunID:                    execution.GetRunId(),
		ParentDomainID:           "",
		ParentWorkflowID:         "",
		ParentRunID:              "",
		InitiatedID:              common.EmptyEventID,
		TaskList:                 event1.WorkflowExecutionStartedEventAttributes.TaskList.GetName(),
		WorkflowTypeName:         event1.WorkflowExecutionStartedEventAttributes.WorkflowType.GetName(),
		WorkflowTimeout:          *event1.WorkflowExecutionStartedEventAttributes.ExecutionStartToCloseTimeoutSeconds,
		DecisionTimeoutValue:     *event1.WorkflowExecutionStartedEventAttributes.TaskStartToCloseTimeoutSeconds,
		State:                    persistence.WorkflowStateCreated,
		CloseStatus:              persistence.WorkflowCloseStatusNone,
		LastFirstEventID:         event1.GetEventId(),
		NextEventID:              nextEventID,
		LastProcessedEvent:       common.EmptyEventID,
		StartTimestamp:           startTime,
		LastUpdatedTimestamp:     startTime,
		DecisionVersion:          common.EmptyVersion,
		DecisionScheduleID:       common.EmptyEventID,
		DecisionStartedID:        common.EmptyEventID,
		DecisionRequestID:        emptyUUID,
		DecisionTimeout:          0,
		DecisionAttempt:          0,
		DecisionStartedTimestamp: 0,
		CreateRequestID:          createRequestID,
	}
	// this is only a shallow test, meaning
	// the mutable state only has the minimal information
	// so we can test the conflict resolver
	s.mockExecutionMgr.On("ResetMutableState", &persistence.ResetMutableStateRequest{
		PrevRunID:     prevRunID,
		ExecutionInfo: executionInfo,
		ReplicationState: &persistence.ReplicationState{
			CurrentVersion:   event1.GetVersion(),
			StartVersion:     event1.GetVersion(),
			LastWriteVersion: event1.GetVersion(),
			LastWriteEventID: event1.GetEventId(),
			LastReplicationInfo: map[string]*persistence.ReplicationInfo{
				sourceCluster: &persistence.ReplicationInfo{
					Version:     event1.GetVersion(),
					LastEventID: event1.GetEventId(),
				},
			},
		},
		Condition:                 s.mockContext.updateCondition,
		RangeID:                   s.mockShard.shardInfo.RangeID,
		InsertActivityInfos:       []*persistence.ActivityInfo{},
		InsertTimerInfos:          []*persistence.TimerInfo{},
		InsertChildExecutionInfos: []*persistence.ChildExecutionInfo{},
		InsertRequestCancelInfos:  []*persistence.RequestCancelInfo{},
		InsertSignalInfos:         []*persistence.SignalInfo{},
		InsertSignalRequestedIDs:  []string{},
		Encoding:                  common.EncodingType(s.mockShard.GetConfig().EventEncodingType(domainID)),
	}).Return(nil).Once()
	s.mockExecutionMgr.On("GetWorkflowExecution", &persistence.GetWorkflowExecutionRequest{
		DomainID:  domainID,
		Execution: execution,
	}).Return(&persistence.GetWorkflowExecutionResponse{}, nil).Once() // return empty resoonse since we are not testing the load
	s.mockClusterMetadata.On("IsGlobalDomainEnabled").Return(true)
	s.mockDomainCache.On("GetDomainByID", mock.Anything).Return(cache.NewDomainCacheEntryForTest(&persistence.DomainInfo{}, nil), nil)
	s.mockEventsCache.On("putEvent", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

	_, err := s.conflictResolver.reset(prevRunID, createRequestID, nextEventID-1, executionInfo)
	s.Nil(err)
}
