package sse

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tonkeeper/opentonapi/pkg/pusher/errors"
	"github.com/tonkeeper/opentonapi/pkg/pusher/events"
	"github.com/tonkeeper/opentonapi/pkg/pusher/sources"
	"github.com/tonkeeper/tongo"
)

// Handler handles http methods for sse.
type Handler struct {
	txSource       sources.TransactionSource
	blockSource    sources.BlockSource
	traceSource    sources.TraceSource
	memPool        sources.MemPoolSource
	currentEventID int64
}

type handlerFunc func(session *session, request *http.Request) error

func NewHandler(blockSource sources.BlockSource, txSource sources.TransactionSource, traceSource sources.TraceSource, memPool sources.MemPoolSource) *Handler {
	h := Handler{
		txSource:       txSource,
		blockSource:    blockSource,
		traceSource:    traceSource,
		memPool:        memPool,
		currentEventID: time.Now().UnixNano(),
	}
	return &h
}
func parseQueryStrings(accountsStr string, operationsStr string) (*sources.SubscribeToTransactionsOptions, error) {
	allAccounts := false
	var accounts []tongo.AccountID

	if strings.ToUpper(accountsStr) == "ALL" {
		allAccounts = true
	} else {
		accountStrings := strings.Split(accountsStr, ",")
		accounts = make([]tongo.AccountID, 0, len(accountStrings))
		for _, account := range accountStrings {
			accountID, err := tongo.ParseAddress(account)
			if err != nil {
				return nil, err
			}
			accounts = append(accounts, accountID.ID)
		}
	}
	allOps := len(operationsStr) == 0
	var operations []string
	if len(operationsStr) > 0 {
		operations = strings.Split(operationsStr, ",")
	}
	options := sources.SubscribeToTransactionsOptions{
		Accounts:      accounts,
		AllAccounts:   allAccounts,
		Operations:    operations,
		AllOperations: allOps,
	}
	return &options, nil
}

func (h *Handler) SubscribeToTransactions(session *session, request *http.Request) error {
	if h.txSource == nil {
		return errors.BadRequest("trace source is not configured")
	}
	query := request.URL.Query()
	options, err := parseQueryStrings(query.Get("accounts"), query.Get("operations"))
	if err != nil {
		return errors.BadRequest(fmt.Sprintf("failed to parse query parameters: %v", err))
	}
	cancelFn := h.txSource.SubscribeToTransactions(request.Context(), func(data []byte) {
		event := Event{
			Name:    events.AccountTxEvent,
			EventID: h.nextID(),
			Data:    data,
		}
		session.SendEvent(event)
	}, *options)
	session.SetCancelFn(cancelFn)
	return nil
}

func (h *Handler) SubscribeToMessages(session *session, request *http.Request) error {
	if h.memPool == nil {
		return errors.BadRequest("mempool source is not configured")
	}
	accountsStr := request.URL.Query().Get("accounts")
	var accounts []tongo.AccountID
	if len(accountsStr) > 0 {
		accountStrings := strings.Split(accountsStr, ",")
		accounts = make([]tongo.AccountID, 0, len(accountStrings))
		for _, account := range accountStrings {
			accountID, err := tongo.ParseAddress(account)
			if err != nil {
				return err
			}
			accounts = append(accounts, accountID.ID)
		}
	}
	cancelFn, err := h.memPool.SubscribeToMessages(request.Context(), func(data []byte) {
		event := Event{
			Name:    events.MempoolEvent,
			EventID: h.nextID(),
			Data:    data,
		}
		session.SendEvent(event)
	}, sources.SubscribeToMempoolOptions{Accounts: accounts})
	if err != nil {
		return err
	}
	session.SetCancelFn(cancelFn)
	return nil
}

func parseAccountsToTraceOptions(str string) (*sources.SubscribeToTraceOptions, error) {
	if strings.ToUpper(str) == "ALL" {
		return &sources.SubscribeToTraceOptions{AllAccounts: true}, nil
	}
	accountStrings := strings.Split(str, ",")
	accounts := make([]tongo.AccountID, 0, len(accountStrings))
	for _, account := range accountStrings {
		accountID, err := tongo.ParseAddress(account)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, accountID.ID)
	}
	return &sources.SubscribeToTraceOptions{Accounts: accounts}, nil
}

func (h *Handler) SubscribeToTraces(session *session, request *http.Request) error {
	if h.traceSource == nil {
		return errors.BadRequest("trace source is not configured")
	}
	accounts := request.URL.Query().Get("accounts")
	options, err := parseAccountsToTraceOptions(accounts)
	if err != nil {
		return errors.BadRequest("failed to parse 'accounts' parameter in query")
	}
	cancelFn := h.traceSource.SubscribeToTraces(request.Context(), func(data []byte) {
		event := Event{
			Name:    events.TraceEvent,
			EventID: h.nextID(),
			Data:    data,
		}
		session.SendEvent(event)
	}, *options)
	session.SetCancelFn(cancelFn)
	return nil
}

func (h *Handler) SubscribeToBlocks(session *session, request *http.Request) error {
	if h.blockSource == nil {
		return errors.BadRequest("block source is not configured")
	}
	workchain := request.URL.Query().Get("workchain")
	opts := sources.SubscribeToBlocksOptions{}
	if len(workchain) > 0 {
		value, err := strconv.Atoi(workchain)
		if err != nil {
			return errors.BadRequest("failed to parse 'workchain' parameter in query")
		}
		if value != -1 && value != 0 {
			return errors.BadRequest("invalid 'workchain' parameter in query")
		}
		opts.Workchain = &value
	}
	cancelFn := h.blockSource.SubscribeToBlocks(request.Context(), func(data []byte) {
		event := Event{
			Name:    events.BlockEvent,
			EventID: h.nextID(),
			Data:    data,
		}
		session.SendEvent(event)
	}, opts)
	session.SetCancelFn(cancelFn)
	return nil
}

func (h *Handler) nextID() int64 {
	return atomic.AddInt64(&h.currentEventID, 1)
}
