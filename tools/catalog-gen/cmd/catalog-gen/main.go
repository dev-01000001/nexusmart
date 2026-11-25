package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nexusmart/core/pkg/common/env"
	"github.com/nexusmart/core/tools/catalog-gen/internal/generator"
	"github.com/nexusmart/core/tools/catalog-gen/internal/loader"
)

type cliConfig struct {
	mode            string
	count           int
	concurrency     int
	seed            uint64
	approve         bool
	dryRun          bool
	gatewayURL      string
	productGRPCAddr string
	shopIDs         string
	jwtSecret       string
	adminEmail      string
	adminPassword   string
	sellerPassword  string
	sellerEmails    string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "catalog-gen error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() cliConfig {
	var cfg cliConfig
	flag.StringVar(&cfg.mode, "mode", "rest", "Ingestion API mode: rest or grpc")
	flag.IntVar(&cfg.count, "count", generator.DefaultCount, "Number of Vietnamese products to generate")
	flag.IntVar(&cfg.concurrency, "concurrency", 16, "Concurrent worker goroutines")
	flag.Uint64Var(&cfg.seed, "seed", generator.DefaultSeed, "Deterministic PRNG seed")
	flag.BoolVar(&cfg.approve, "approve", true, "Admin-approve created products so they become searchable")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "Generate and validate locally without network calls")
	flag.StringVar(&cfg.gatewayURL, "gateway-url", env.EnvString("GATEWAY_URL", "http://localhost:28080"), "Gateway base URL for REST auth/discovery/ingestion")
	flag.StringVar(&cfg.productGRPCAddr, "product-grpc-addr", env.EnvString("PRODUCT_GRPC_ADDR", "localhost:50052"), "ProductService gRPC address for --mode=grpc")
	flag.StringVar(&cfg.shopIDs, "shop-ids", "", "Comma-separated shop IDs for isolated gRPC mode or dry-run override")
	flag.StringVar(&cfg.jwtSecret, "jwt-secret", env.EnvString("JWT_SECRET", ""), "HS256 JWT secret for isolated gRPC mode (used when --shop-ids is set)")
	flag.StringVar(&cfg.adminEmail, "admin-email", env.EnvString("ADMIN_BOOTSTRAP_EMAIL", "admin@nexusmart.local"), "Admin login email")
	flag.StringVar(&cfg.adminPassword, "admin-password", env.EnvString("ADMIN_BOOTSTRAP_PASSWORD", "AdminDevPassword123!"), "Admin login password")
	flag.StringVar(&cfg.sellerPassword, "seller-password", env.EnvString("SEED_SELLER_PASSWORD", "SellerDemo123!"), "Seeded seller login password")
	flag.StringVar(&cfg.sellerEmails, "seller-emails", "", "Optional comma-separated seller emails override")
	flag.Parse()
	return cfg
}

func run() error {
	cfg := parseFlags()
	cfg.mode = strings.ToLower(strings.TrimSpace(cfg.mode))
	if cfg.mode != "rest" && cfg.mode != "grpc" {
		return fmt.Errorf("invalid --mode %q (expected rest or grpc)", cfg.mode)
	}
	if cfg.count <= 0 {
		return fmt.Errorf("--count must be > 0 (got %d)", cfg.count)
	}
	if cfg.concurrency <= 0 {
		cfg.concurrency = 1
	}

	if cfg.dryRun {
		return runDryRun(cfg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sellers := buildSellerAccounts(cfg)
	adminCreds := loader.AdminCredentials{
		Email:    cfg.adminEmail,
		Password: cfg.adminPassword,
	}

	onProgress := func(p loader.Progress) {
		rate := 0.0
		if p.Elapsed > 0 {
			rate = float64(p.Created) / p.Elapsed.Seconds()
		}
		fmt.Fprintf(os.Stderr, "[catalog-gen] progress %d/%d created=%d approved=%d failed=%d elapsed=%s rate=%.1f/s\n",
			p.Processed, p.Total, p.Created, p.Approved, p.Failed, p.Elapsed.Round(time.Millisecond), rate)
	}

	switch cfg.mode {
	case "rest":
		return runREST(ctx, cfg, adminCreds, sellers, onProgress)
	case "grpc":
		return runGRPC(ctx, cfg, adminCreds, sellers, onProgress)
	default:
		return fmt.Errorf("unsupported mode %q", cfg.mode)
	}
}

func runDryRun(cfg cliConfig) error {
	start := time.Now()
	shops := generator.DefaultShops()
	if ids := splitCSV(cfg.shopIDs); len(ids) > 0 {
		shops = make([]generator.ShopTarget, 0, len(ids))
		for _, id := range ids {
			shops = append(shops, generator.ShopTarget{ID: id, Slug: id, Name: id})
		}
	}
	cats := generator.DefaultCategories()

	gen := generator.New(cfg.seed, shops, cats)
	products := gen.Generate(cfg.count)

	shopCounts := make(map[string]int, len(shops))
	catCounts := make(map[string]int, len(cats))
	totalSKUs := 0
	outOfStock := 0

	for i, p := range products {
		if strings.TrimSpace(p.Name) == "" || len(p.SKUs) == 0 {
			return fmt.Errorf("dry-run validation failed at product[%d]", i)
		}
		shopCounts[p.ShopID]++
		catCounts[p.CategoryID]++
		seen := make(map[string]bool, len(p.SKUs))
		allZero := true
		for _, sk := range p.SKUs {
			if seen[sk.SKUCode] {
				return fmt.Errorf("dry-run duplicate sku_code %q on product[%d]", sk.SKUCode, i)
			}
			seen[sk.SKUCode] = true
			if sk.PriceVND < generator.MinPriceVND || sk.PriceVND > generator.MaxPriceVND || sk.PriceVND%generator.PriceStep != 0 {
				return fmt.Errorf("dry-run invalid price %d on product[%d]", sk.PriceVND, i)
			}
			if sk.Stock > 0 {
				allZero = false
			}
			totalSKUs++
		}
		if allZero {
			outOfStock++
		}
	}

	elapsed := time.Since(start)
	rate := 0.0
	if elapsed > 0 {
		rate = math.Round((float64(len(products))/elapsed.Seconds())*100) / 100
	}

	summary := loader.Summary{
		Mode:           cfg.mode,
		DryRun:         true,
		Seed:           cfg.seed,
		Generated:      len(products),
		Created:        0,
		Approved:       0,
		Failed:         0,
		ElapsedMs:      elapsed.Milliseconds(),
		RatePerSec:     rate,
		Shops:          len(shops),
		Categories:     len(cats),
		TotalSKUs:      totalSKUs,
		OutOfStock:     outOfStock,
		ShopCounts:     shopCounts,
		CategoryCounts: catCounts,
	}
	return writeSummary(summary)
}

func runREST(
	ctx context.Context,
	cfg cliConfig,
	adminCreds loader.AdminCredentials,
	sellers []loader.SellerAccount,
	onProgress func(loader.Progress),
) error {
	restCli := loader.NewRESTClient(cfg.gatewayURL, cfg.concurrency)
	fmt.Fprintf(os.Stderr, "[catalog-gen] discovering auth, shops, categories via %s\n", cfg.gatewayURL)
	disc, err := restCli.Discover(ctx, adminCreds, sellers, cfg.approve, true)
	if err != nil {
		return err
	}
	for _, skipped := range disc.Skipped {
		fmt.Fprintf(os.Stderr, "[catalog-gen] skipped seller: %s\n", skipped)
	}
	fmt.Fprintf(os.Stderr, "[catalog-gen] discovered %d shops and %d categories; generating %d products (seed=%d, concurrency=%d, approve=%t)\n",
		len(disc.Shops), len(disc.Categories), cfg.count, cfg.seed, cfg.concurrency, cfg.approve)

	gen := generator.New(cfg.seed, disc.Shops, disc.Categories)
	products := gen.Generate(cfg.count)

	summary := loader.RunIngest(ctx, "rest", cfg.seed, products, cfg.concurrency, cfg.approve, restCli, onProgress)
	summary.Shops = len(disc.Shops)
	summary.Categories = len(disc.Categories)
	if err := writeSummary(summary); err != nil {
		return err
	}
	if summary.Failed > 0 {
		return fmt.Errorf("completed with %d failed products", summary.Failed)
	}
	return nil
}

func runGRPC(
	ctx context.Context,
	cfg cliConfig,
	adminCreds loader.AdminCredentials,
	sellers []loader.SellerAccount,
	onProgress func(loader.Progress),
) error {
	var (
		adminToken  string
		adminUserID string
		shops       []generator.ShopTarget
		shopTokens  map[string]string
		isolated    bool
	)

	if explicitShops := splitCSV(cfg.shopIDs); len(explicitShops) > 0 {
		if strings.TrimSpace(cfg.jwtSecret) == "" {
			return errors.New("--jwt-secret (or JWT_SECRET) is required when using --shop-ids in isolated gRPC mode")
		}
		var err error
		adminToken, adminUserID, shops, shopTokens, err = loader.MintIsolatedTokens(cfg.jwtSecret, explicitShops)
		if err != nil {
			return err
		}
		isolated = true
		fmt.Fprintf(os.Stderr, "[catalog-gen] isolated gRPC mode: minted JWTs for %d shops targeting %s\n", len(shops), cfg.productGRPCAddr)
	} else {
		restCli := loader.NewRESTClient(cfg.gatewayURL, cfg.concurrency)
		fmt.Fprintf(os.Stderr, "[catalog-gen] logging in admin & sellers via %s for gRPC target %s\n", cfg.gatewayURL, cfg.productGRPCAddr)
		disc, err := restCli.Discover(ctx, adminCreds, sellers, cfg.approve, false)
		if err != nil {
			return err
		}
		for _, skipped := range disc.Skipped {
			fmt.Fprintf(os.Stderr, "[catalog-gen] skipped seller: %s\n", skipped)
		}
		adminToken = disc.AdminToken
		adminUserID = disc.AdminUserID
		shops = disc.Shops
		shopTokens = disc.ShopTokens
	}

	grpcCli, err := loader.NewGRPCClient(cfg.productGRPCAddr, adminToken, adminUserID, shopTokens)
	if err != nil {
		return err
	}
	defer func() { _ = grpcCli.Close() }()

	cats, err := grpcCli.ListCategories(ctx, isolated)
	if err != nil {
		return err
	}
	if len(cats) == 0 {
		return errors.New("ProductService.ListCategories returned 0 categories; seed categories first")
	}

	fmt.Fprintf(os.Stderr, "[catalog-gen] discovered %d shops and %d categories; generating %d products via gRPC (seed=%d, concurrency=%d, approve=%t)\n",
		len(shops), len(cats), cfg.count, cfg.seed, cfg.concurrency, cfg.approve)

	gen := generator.New(cfg.seed, shops, cats)
	products := gen.Generate(cfg.count)

	summary := loader.RunIngest(ctx, "grpc", cfg.seed, products, cfg.concurrency, cfg.approve, grpcCli, onProgress)
	summary.Shops = len(shops)
	summary.Categories = len(cats)
	if err := writeSummary(summary); err != nil {
		return err
	}
	if summary.Failed > 0 {
		return fmt.Errorf("completed with %d failed products", summary.Failed)
	}
	return nil
}

func buildSellerAccounts(cfg cliConfig) []loader.SellerAccount {
	if custom := splitCSV(cfg.sellerEmails); len(custom) > 0 {
		out := make([]loader.SellerAccount, 0, len(custom))
		for i, email := range custom {
			out = append(out, loader.SellerAccount{
				Slug:     fmt.Sprintf("shop-%d", i+1),
				Email:    email,
				Password: cfg.sellerPassword,
			})
		}
		return out
	}
	return []loader.SellerAccount{
		{
			Slug:     "shop-bach-dang",
			Email:    env.EnvString("SEED_SELLER_EMAIL", "seller@nexusmart.local"),
			Password: cfg.sellerPassword,
		},
		{
			Slug:     "shop-sai-gon",
			Email:    env.EnvString("SEED_SELLER_SAIGON_EMAIL", "seller-saigon@nexusmart.local"),
			Password: cfg.sellerPassword,
		},
		{
			Slug:     "shop-da-nang",
			Email:    env.EnvString("SEED_SELLER_DANANG_EMAIL", "seller-danang@nexusmart.local"),
			Password: cfg.sellerPassword,
		},
		{
			Slug:     "shop-ha-noi",
			Email:    env.EnvString("SEED_SELLER_HANOI_EMAIL", "seller-hanoi@nexusmart.local"),
			Password: cfg.sellerPassword,
		},
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func writeSummary(s loader.Summary) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}
