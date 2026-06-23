package main

import (
	"fmt"
	"log"
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

	// ── Tenant ───────────────────────────────────────────────
	tenant := models.Tenant{
		Name: "Whatify Demo",
		Plan: models.PlanGrowth,
	}
	if err := db.Create(&tenant).Error; err != nil {
		log.Fatalf("create tenant: %v", err)
	}
	fmt.Printf("✅ Tenant:  %s  (id: %s)\n", tenant.Name, tenant.ID)

	// ── Admin user ───────────────────────────────────────────
	adminHash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	admin := models.User{
		TenantID:     tenant.ID,
		Name:         "Ahmed Admin",
		Email:        "admin@demo.com",
		PasswordHash: string(adminHash),
		Role:         models.RoleAdmin,
	}
	if err := db.Create(&admin).Error; err != nil {
		log.Fatalf("create admin: %v", err)
	}
	fmt.Printf("✅ Admin:   %s  /  admin@demo.com  /  admin123\n", admin.Name)

	// ── Agent user ───────────────────────────────────────────
	agentHash, _ := bcrypt.GenerateFromPassword([]byte("agent123"), bcrypt.DefaultCost)
	agent := models.User{
		TenantID:     tenant.ID,
		Name:         "Sara Agent",
		Email:        "agent@demo.com",
		PasswordHash: string(agentHash),
		Role:         models.RoleAgent,
	}
	if err := db.Create(&agent).Error; err != nil {
		log.Fatalf("create agent: %v", err)
	}
	fmt.Printf("✅ Agent:   %s  /  agent@demo.com  /  agent123\n", agent.Name)

	fmt.Println("\n🎉 Seed done!")
	fmt.Println("──────────────────────────────────────")
	fmt.Printf("Tenant ID : %s\n", tenant.ID)
	fmt.Printf("Admin  ID : %s\n", admin.ID)
	fmt.Printf("Agent  ID : %s\n", agent.ID)
}
