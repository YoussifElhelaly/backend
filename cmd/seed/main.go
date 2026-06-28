// Command seed populates the database with a ready-to-use test account and
// catalog data (tags, products, quick replies). Safe to re-run: it skips
// seeding when the admin user already exists.
//
//	go run ./cmd/seed
package main

import (
	"fmt"
	"log"
	"time"

	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file, using env vars")
	}

	database.Connect()
	db := database.DB

	// Idempotent: bail out if the demo admin already exists.
	var existing models.User
	if err := db.Where("email = ?", "admin@demo.com").First(&existing).Error; err == nil {
		log.Println("admin@demo.com already exists — nothing to seed")
		return
	}

	// ── Tenant (Scale plan so plan limits don't block testing) ──
	farFuture := time.Now().AddDate(10, 0, 0)
	tenant := models.Tenant{
		Name:        "Whatify Demo",
		Plan:        models.PlanScale,
		TrialEndsAt: &farFuture,
	}
	if err := db.Create(&tenant).Error; err != nil {
		log.Fatalf("create tenant: %v", err)
	}
	fmt.Printf("✅ Tenant:  %s  (id: %s)\n", tenant.Name, tenant.ID)

	// ── Admin user (email verified so login works) ──────────────
	adminHash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	admin := models.User{
		TenantID:        tenant.ID,
		Name:            "Ahmed Admin",
		Email:           "admin@demo.com",
		PasswordHash:    string(adminHash),
		Role:            models.RoleAdmin,
		IsEmailVerified: true,
	}
	if err := db.Create(&admin).Error; err != nil {
		log.Fatalf("create admin: %v", err)
	}
	fmt.Printf("✅ Admin:   %s  /  admin@demo.com  /  admin123\n", admin.Name)

	// ── Agent user (email verified so login works) ──────────────
	agentHash, _ := bcrypt.GenerateFromPassword([]byte("agent123"), bcrypt.DefaultCost)
	agent := models.User{
		TenantID:        tenant.ID,
		Name:            "Sara Agent",
		Email:           "agent@demo.com",
		PasswordHash:    string(agentHash),
		Role:            models.RoleAgent,
		IsEmailVerified: true,
	}
	if err := db.Create(&agent).Error; err != nil {
		log.Fatalf("create agent: %v", err)
	}
	fmt.Printf("✅ Agent:   %s  /  agent@demo.com  /  agent123\n", agent.Name)

	// ── Tags ────────────────────────────────────────────────────
	tags := []models.Tag{
		{TenantID: tenant.ID, Name: "VIP", Color: "#F59E0B"},
		{TenantID: tenant.ID, Name: "Lead", Color: "#3B82F6"},
		{TenantID: tenant.ID, Name: "Customer", Color: "#10B981"},
		{TenantID: tenant.ID, Name: "Support", Color: "#EF4444"},
	}
	if err := db.Create(&tags).Error; err != nil {
		log.Fatalf("create tags: %v", err)
	}
	fmt.Printf("✅ Tags:    %d\n", len(tags))

	// ── Products (sample catalog — real products with image + link) ──
	products := []models.Product{
		{TenantID: tenant.ID, Name: "Wireless Headphones", Price: 1200, Description: "Noise-cancelling over-ear headphones, 30h battery.", ImageURL: "https://images.unsplash.com/photo-1505740420928-5e560c06d30e?w=400", Link: "https://shop.example.com/headphones"},
		{TenantID: tenant.ID, Name: "Smart Watch", Price: 2500, Description: "Fitness tracking, heart-rate monitor, AMOLED display.", ImageURL: "https://images.unsplash.com/photo-1546868871-7041f2a55e12?w=400", Link: "https://shop.example.com/smart-watch"},
		{TenantID: tenant.ID, Name: "Leather Backpack", Price: 850, Description: "Genuine leather, fits a 15\" laptop, water-resistant.", ImageURL: "https://images.unsplash.com/photo-1553062407-98eeb64c6a62?w=400", Link: "https://shop.example.com/backpack"},
	}
	if err := db.Create(&products).Error; err != nil {
		log.Fatalf("create products: %v", err)
	}
	fmt.Printf("✅ Products: %d\n", len(products))

	// ── Quick replies ───────────────────────────────────────────
	replies := []models.QuickReply{
		{TenantID: tenant.ID, Title: "Greeting", Content: "Hello {name}! 👋 How can we help you today?"},
		{TenantID: tenant.ID, Title: "Hours", Content: "We're available Sun–Thu, 9 AM – 6 PM."},
		{TenantID: tenant.ID, Title: "Thanks", Content: "Thank you for reaching out! We'll get back to you shortly."},
	}
	if err := db.Create(&replies).Error; err != nil {
		log.Fatalf("create quick replies: %v", err)
	}
	fmt.Printf("✅ QuickReplies: %d\n", len(replies))

	fmt.Println("\n🎉 Seed done!")
	fmt.Println("──────────────────────────────────────")
	fmt.Printf("Tenant ID : %s\n", tenant.ID)
	fmt.Printf("Login     : admin@demo.com / admin123  (or agent@demo.com / agent123)\n")
}
