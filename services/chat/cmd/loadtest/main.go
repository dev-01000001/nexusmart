package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	chatv1 "github.com/nexusmart/core/gen/go/nexusmart/chat/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type wsEnvelope struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Message   *struct {
		ID          string `json:"id"`
		Body        string `json:"body"`
		ClientMsgID string `json:"client_msg_id"`
	} `json:"message"`
}

func main() {
	var (
		wsURL          = flag.String("url", "ws://127.0.0.1:8090/ws", "WebSocket endpoint URL")
		grpcAddr       = flag.String("grpc-addr", "", "Optional Chat gRPC address (e.g. 127.0.0.1:50060) to auto-create a real conversation for end-to-end message delivery")
		conversationID = flag.String("conversation-id", "", "Optional existing conversation ID for loadtest-buyer-0 / loadtest-shop-0")
		connsCount     = flag.Int("conns", 1000, "Number of simultaneous WebSocket connections to hold open")
		jwtSecret      = flag.String("jwt-secret", "dev-only-nexusmart-hs256-secret-change-me", "HS256 JWT secret matching chat server")
		messagesCount  = flag.Int("messages", 100, "Number of real messages to send/receive while all connections are active")
		holdDuration   = flag.Duration("hold", 3*time.Second, "Duration to hold all connections concurrently open")
		dialConc       = flag.Int("concurrency", 100, "Max concurrent WebSocket dials during ramp-up")
	)
	flag.Parse()

	if *connsCount < 2 {
		fmt.Fprintln(os.Stderr, "conns must be >= 2")
		os.Exit(1)
	}
	if *dialConc < 1 {
		*dialConc = 100
	}

	jwtMgr, err := auth.NewManager(*jwtSecret, 15*time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init jwt manager: %v\n", err)
		os.Exit(1)
	}

	runID := uuid.NewString()[:8]
	buyer0ID := "loadtest-buyer-" + runID + "-0"
	seller0ID := "loadtest-seller-" + runID + "-0"
	shop0ID := "loadtest-shop-" + runID + "-0"

	buyer0Tok, _, _, err := jwtMgr.SignAccessToken(buyer0ID, "buyer", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign buyer0 token: %v\n", err)
		os.Exit(1)
	}
	seller0Tok, _, _, err := jwtMgr.SignAccessToken(seller0ID, "seller", shop0ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign seller0 token: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute+*holdDuration)
	defer cancel()

	convID := *conversationID
	if convID == "" && *grpcAddr != "" {
		convID, err = createConversationViaGRPC(ctx, *grpcAddr, buyer0Tok, shop0ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create conversation via gRPC %s: %v\n", *grpcAddr, err)
			os.Exit(1)
		}
	}

	type activeConn struct {
		conn *websocket.Conn
	}

	conns := make([]*activeConn, *connsCount)
	connectLatencies := make([]time.Duration, 0, *connsCount)
	var (
		latMu         sync.Mutex
		activeCount   atomic.Int64
		activePeak    atomic.Int64
		connectErrors atomic.Int64
		readDrops     atomic.Int64
	)

	// Channels for measuring real end-to-end delivery latency.
	sentTimes := make(map[string]time.Time, *messagesCount)
	var sentMu sync.Mutex
	deliveryLatencies := make([]time.Duration, 0, *messagesCount)
	var deliveryMu sync.Mutex
	deliveredCh := make(chan string, *messagesCount+16)

	sem := make(chan struct{}, *dialConc)
	var dialWG sync.WaitGroup
	var readerWG sync.WaitGroup

	rampStart := time.Now()
	for i := 0; i < *connsCount; i++ {
		i := i
		dialWG.Add(1)
		sem <- struct{}{}
		go func() {
			defer dialWG.Done()
			defer func() { <-sem }()

			var tok string
			switch i {
			case 0:
				tok = buyer0Tok
			case 1:
				tok = seller0Tok
			default:
				var signErr error
				tok, _, _, signErr = jwtMgr.SignAccessToken("loadtest-user-"+runID+"-"+strconv.Itoa(i), "buyer", "")
				if signErr != nil {
					connectErrors.Add(1)
					return
				}
			}

			t0 := time.Now()
			dCtx, dCancel := context.WithTimeout(ctx, 15*time.Second)
			c, resp, dialErr := websocket.Dial(dCtx, *wsURL, &websocket.DialOptions{
				CompressionMode: websocket.CompressionDisabled,
				HTTPHeader:      http.Header{"Authorization": []string{"Bearer " + tok}},
			})
			dCancel()
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if dialErr != nil {
				connectErrors.Add(1)
				return
			}

			// Wait for server "connected" handshake frame.
			rCtx, rCancel := context.WithTimeout(ctx, 10*time.Second)
			_, raw, rErr := c.Read(rCtx)
			rCancel()
			if rErr != nil {
				_ = c.CloseNow()
				connectErrors.Add(1)
				return
			}
			var env wsEnvelope
			if err := json.Unmarshal(raw, &env); err != nil || env.Type != "connected" {
				_ = c.CloseNow()
				connectErrors.Add(1)
				return
			}

			elapsed := time.Since(t0)
			latMu.Lock()
			connectLatencies = append(connectLatencies, elapsed)
			latMu.Unlock()

			conns[i] = &activeConn{conn: c}
			cur := activeCount.Add(1)
			for {
				prev := activePeak.Load()
				if cur <= prev || activePeak.CompareAndSwap(prev, cur) {
					break
				}
			}

			// Start background reader for every socket so pongs/messages are drained cleanly.
			readerWG.Add(1)
			go func(idx int, wc *websocket.Conn) {
				defer readerWG.Done()
				defer activeCount.Add(-1)
				for {
					_, msgBytes, err := wc.Read(ctx)
					if err != nil {
						return
					}
					if idx == 1 && convID != "" {
						var e wsEnvelope
						if json.Unmarshal(msgBytes, &e) == nil && e.Type == "message" && e.Message != nil && e.Message.ClientMsgID != "" {
							sentMu.Lock()
							tSent, ok := sentTimes[e.Message.ClientMsgID]
							sentMu.Unlock()
							if ok {
								deliveryMu.Lock()
								deliveryLatencies = append(deliveryLatencies, time.Since(tSent))
								deliveryMu.Unlock()
								select {
								case deliveredCh <- e.Message.ClientMsgID:
								default:
								}
							}
						}
					} else if idx == 0 && convID == "" {
						var e wsEnvelope
						if json.Unmarshal(msgBytes, &e) == nil && e.Type == "pong" && e.RequestID != "" {
							sentMu.Lock()
							tSent, ok := sentTimes[e.RequestID]
							sentMu.Unlock()
							if ok {
								deliveryMu.Lock()
								deliveryLatencies = append(deliveryLatencies, time.Since(tSent))
								deliveryMu.Unlock()
								select {
								case deliveredCh <- e.RequestID:
								default:
								}
							}
						}
					}
				}
			}(i, c)
		}()
	}
	dialWG.Wait()
	rampElapsed := time.Since(rampStart)

	var deliveryErrors atomic.Int64
	deliveredCount := 0
	mode := "ping_pong"
	if convID != "" {
		mode = "chat_message_delivery"
	}

	if connectErrors.Load() == 0 && conns[0] != nil && conns[1] != nil && *messagesCount > 0 {
		for i := 0; i < *messagesCount; i++ {
			msgKey := fmt.Sprintf("lt-%s-%d", runID, i)
			var payload []byte
			if convID != "" {
				payload, _ = json.Marshal(map[string]any{
					"type":            "send",
					"request_id":      msgKey,
					"conversation_id": convID,
					"kind":            "text",
					"body":            "loadtest message " + strconv.Itoa(i),
					"client_msg_id":   msgKey,
				})
			} else {
				payload, _ = json.Marshal(map[string]any{
					"type":       "ping",
					"request_id": msgKey,
				})
			}

			sentMu.Lock()
			sentTimes[msgKey] = time.Now()
			sentMu.Unlock()

			wCtx, wCancel := context.WithTimeout(ctx, 5*time.Second)
			err := conns[0].conn.Write(wCtx, websocket.MessageText, payload)
			wCancel()
			if err != nil {
				deliveryErrors.Add(1)
			}
		}

		deadline := time.NewTimer(15 * time.Second)
		for deliveredCount < *messagesCount {
			select {
			case <-deliveredCh:
				deliveredCount++
			case <-deadline.C:
				deliveryErrors.Add(int64(*messagesCount - deliveredCount))
				goto holdPhase
			}
		}
		deadline.Stop()
	}

holdPhase:
	if *holdDuration > 0 {
		time.Sleep(*holdDuration)
	}
	activeAtHoldEnd := activeCount.Load()
	if activeAtHoldEnd < int64(*connsCount) && connectErrors.Load() == 0 {
		readDrops.Add(int64(*connsCount) - activeAtHoldEnd)
	}

	// Cleanly close all connections.
	cancel()
	for _, ac := range conns {
		if ac != nil && ac.conn != nil {
			_ = ac.conn.Close(websocket.StatusNormalClosure, "loadtest complete")
		}
	}
	readerWG.Wait()

	connP50, connP95, connP99, connMax := percentiles(connectLatencies)
	delP50, delP95, delP99, delMax := percentiles(deliveryLatencies)

	fmt.Printf("=== Chat WebSocket Load Test Report ===\n")
	fmt.Printf("target_url        : %s\n", *wsURL)
	fmt.Printf("delivery_mode     : %s\n", mode)
	fmt.Printf("target_conns      : %d\n", *connsCount)
	fmt.Printf("active_peak       : %d\n", activePeak.Load())
	fmt.Printf("active_after_hold : %d (hold=%s)\n", activeAtHoldEnd, holdDuration.String())
	fmt.Printf("ramp_duration     : %s\n", rampElapsed.Round(time.Millisecond))
	fmt.Printf("connect_latency   : p50=%s p95=%s p99=%s max=%s\n", connP50, connP95, connP99, connMax)
	fmt.Printf("messages          : sent=%d delivered=%d errors=%d\n", *messagesCount, deliveredCount, deliveryErrors.Load())
	fmt.Printf("delivery_latency  : p50=%s p95=%s p99=%s max=%s\n", delP50, delP95, delP99, delMax)
	fmt.Printf("connect_errors    : %d\n", connectErrors.Load())
	fmt.Printf("unexpected_drops  : %d\n", readDrops.Load())

	if connectErrors.Load() > 0 || activePeak.Load() < int64(*connsCount) || deliveryErrors.Load() > 0 || readDrops.Load() > 0 {
		os.Exit(1)
	}
}

func createConversationViaGRPC(ctx context.Context, addr, bearerTok, shopID string) (string, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	client := chatv1.NewChatServiceClient(conn)
	callCtx, cancel := context.WithTimeout(auth.InjectOutgoingGRPC(ctx, bearerTok), 10*time.Second)
	defer cancel()

	resp, err := client.CreateOrGetConversation(callCtx, &chatv1.CreateOrGetConversationRequest{
		ShopId:   shopID,
		SiteHost: "loadtest.nexusmart.localhost",
	})
	if err != nil {
		return "", err
	}
	return resp.GetConversation().GetId(), nil
}

func percentiles(vals []time.Duration) (p50, p95, p99, maxVal time.Duration) {
	if len(vals) == 0 {
		return 0, 0, 0, 0
	}
	cp := make([]time.Duration, len(vals))
	copy(cp, vals)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })

	pick := func(p float64) time.Duration {
		idx := int(float64(len(cp)-1) * p)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(cp) {
			idx = len(cp) - 1
		}
		return cp[idx].Round(time.Microsecond)
	}
	return pick(0.50), pick(0.95), pick(0.99), cp[len(cp)-1].Round(time.Microsecond)
}
