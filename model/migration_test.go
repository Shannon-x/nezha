package model

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// legacyService 模拟升级前的 services 表结构（仅含迁移相关列）。
type legacyService struct {
	ID                  uint64 `gorm:"primaryKey"`
	Name                string
	EnableShowInService *bool
}

func (legacyService) TableName() string { return "services" }

func TestMigrateServiceHideForGuestPreservesHiddenServices(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&legacyService{}); err != nil {
		t.Fatalf("migrate legacy: %v", err)
	}
	public, hidden := true, false
	for _, s := range []legacyService{
		{ID: 1, Name: "public", EnableShowInService: &public},
		{ID: 2, Name: "hidden", EnableShowInService: &hidden},
		{ID: 3, Name: "null"},
	} {
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if !NeedsServiceHideForGuestMigration(db) {
		t.Fatal("legacy schema must require migration")
	}
	if err := db.AutoMigrate(&Service{}); err != nil {
		t.Fatalf("migrate service: %v", err)
	}
	if NeedsServiceHideForGuestMigration(db) {
		t.Fatal("migration must not be required once hide_for_guest exists")
	}
	if err := MigrateServiceHideForGuest(db); err != nil {
		t.Fatalf("MigrateServiceHideForGuest: %v", err)
	}

	want := map[uint64]bool{1: false, 2: true, 3: true}
	for id, wantHidden := range want {
		var got bool
		if err := db.Raw("SELECT hide_for_guest FROM services WHERE id = ?", id).Scan(&got).Error; err != nil {
			t.Fatalf("read service %d: %v", id, err)
		}
		if got != wantHidden {
			t.Errorf("service %d hide_for_guest = %v, want %v", id, got, wantHidden)
		}
	}
}

func TestNeedsServiceHideForGuestMigrationFreshInstall(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if NeedsServiceHideForGuestMigration(db) {
		t.Fatal("fresh install without services table must not require migration")
	}
	if err := db.AutoMigrate(&Service{}); err != nil {
		t.Fatalf("migrate service: %v", err)
	}
	if NeedsServiceHideForGuestMigration(db) {
		t.Fatal("fresh schema must not require migration")
	}
}
