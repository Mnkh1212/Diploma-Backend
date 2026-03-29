package models

import (
	"time"
)

type User struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"not null"`
	Email     string    `json:"email" gorm:"uniqueIndex;not null"`
	Phone     string    `json:"phone"`
	Password  string    `json:"-" gorm:"not null"`
	Avatar    string    `json:"avatar"`
	Currency  string    `json:"currency" gorm:"default:USD"`
	PushToken string    `json:"push_token"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Account struct {
	ID      uint    `json:"id" gorm:"primaryKey"`
	UserID  uint    `json:"user_id" gorm:"not null;index"`
	Name    string  `json:"name" gorm:"not null"`
	Type    string  `json:"type" gorm:"not null"` // bank, cash, credit_card, investment
	Balance float64 `json:"balance" gorm:"default:0"`
	Icon    string  `json:"icon"`
	Color   string  `json:"color"`
	User    User    `json:"-" gorm:"foreignKey:UserID"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Category struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Name  string `json:"name" gorm:"not null"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
	Type  string `json:"type" gorm:"not null"` // income, expense
}

type Transaction struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	UserID      uint      `json:"user_id" gorm:"not null;index"`
	AccountID   uint      `json:"account_id" gorm:"not null;index"`
	CategoryID  uint      `json:"category_id" gorm:"not null;index"`
	Amount      float64   `json:"amount" gorm:"not null"`
	Type        string    `json:"type" gorm:"not null"` // income, expense
	Description string    `json:"description"`
	Date        time.Time `json:"date" gorm:"not null;index"`
	User        User      `json:"-" gorm:"foreignKey:UserID"`
	Account     Account   `json:"account" gorm:"foreignKey:AccountID"`
	Category    Category  `json:"category" gorm:"foreignKey:CategoryID"`
	CreatedAt   time.Time `json:"created_at"`
}

type Budget struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	UserID     uint      `json:"user_id" gorm:"not null;index"`
	CategoryID uint      `json:"category_id" gorm:"index"`
	Amount     float64   `json:"amount" gorm:"not null"`
	Spent      float64   `json:"spent" gorm:"default:0"`
	Month      int       `json:"month" gorm:"not null"`
	Year       int       `json:"year" gorm:"not null"`
	User       User      `json:"-" gorm:"foreignKey:UserID"`
	Category   Category  `json:"category" gorm:"foreignKey:CategoryID"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ScheduledPayment struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	UserID      uint      `json:"user_id" gorm:"not null;index"`
	CategoryID  uint      `json:"category_id" gorm:"not null"`
	AccountID   uint      `json:"account_id" gorm:"not null"`
	Amount      float64   `json:"amount" gorm:"not null"`
	Description string    `json:"description" gorm:"not null"`
	Frequency   string    `json:"frequency" gorm:"not null"` // daily, weekly, monthly, yearly
	NextDate    time.Time `json:"next_date" gorm:"not null"`
	IsActive    bool      `json:"is_active" gorm:"default:true"`
	User        User      `json:"-" gorm:"foreignKey:UserID"`
	Category    Category  `json:"category" gorm:"foreignKey:CategoryID"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AIChat struct {
	ID        uint        `json:"id" gorm:"primaryKey"`
	UserID    uint        `json:"user_id" gorm:"not null;index"`
	Title     string      `json:"title" gorm:"not null"`
	Messages  []AIMessage `json:"messages" gorm:"foreignKey:ChatID"`
	User      User        `json:"-" gorm:"foreignKey:UserID"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

type AIMessage struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	ChatID    uint      `json:"chat_id" gorm:"not null;index"`
	Role      string    `json:"role" gorm:"not null"` // user, assistant
	Content   string    `json:"content" gorm:"type:text;not null"`
	CreatedAt time.Time `json:"created_at"`
}

type ActivityLog struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"not null;index"`
	Action    string    `json:"action" gorm:"not null"`                        // e.g. "create_transaction", "login", "view_dashboard"
	Entity    string    `json:"entity"`                                        // e.g. "transaction", "budget", "account"
	EntityID  uint      `json:"entity_id"`                                     // ID of the affected entity
	Details   string    `json:"details" gorm:"type:text"`                      // JSON string with extra info
	Status    string    `json:"status" gorm:"not null;default:success"`        // success, failed
	IPAddress string    `json:"ip_address"`
	User      User      `json:"-" gorm:"foreignKey:UserID"`
	CreatedAt time.Time `json:"created_at"`
}

type Notification struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"not null;index"`
	Title     string    `json:"title" gorm:"not null"`
	Body      string    `json:"body" gorm:"type:text;not null"`
	Type      string    `json:"type" gorm:"not null"` // insight, warning, tip
	IsRead    bool      `json:"is_read" gorm:"default:false"`
	User      User      `json:"-" gorm:"foreignKey:UserID"`
	CreatedAt time.Time `json:"created_at"`
}

// Request/Response DTOs

type RegisterRequest struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type CreateTransactionRequest struct {
	AccountID   uint    `json:"account_id" binding:"required"`
	CategoryID  uint    `json:"category_id" binding:"required"`
	Amount      float64 `json:"amount" binding:"required"`
	Type        string  `json:"type" binding:"required,oneof=income expense"`
	Description string  `json:"description"`
	Date        string  `json:"date" binding:"required"`
}

type CreateBudgetRequest struct {
	CategoryID uint    `json:"category_id"`
	Amount     float64 `json:"amount" binding:"required"`
	Month      int     `json:"month" binding:"required"`
	Year       int     `json:"year" binding:"required"`
}

type AIChatRequest struct {
	Message string `json:"message" binding:"required"`
	ChatID  uint   `json:"chat_id"`
}

type DashboardResponse struct {
	Balance          float64              `json:"balance"`
	TotalIncome      float64              `json:"total_income"`
	TotalExpenses    float64              `json:"total_expenses"`
	SavingsPercent   float64              `json:"savings_percent"`
	SavingsAmount    float64              `json:"savings_amount"`
	Accounts         []Account            `json:"accounts"`
	RecentTransactions []Transaction      `json:"recent_transactions"`
}

type ExpensesSummary struct {
	Total      float64           `json:"total"`
	Categories []CategoryExpense `json:"categories"`
}

type CategoryExpense struct {
	CategoryID   uint    `json:"category_id"`
	CategoryName string  `json:"category_name"`
	Color        string  `json:"color"`
	Icon         string  `json:"icon"`
	Amount       float64 `json:"amount"`
	Percentage   float64 `json:"percentage"`
}

type StatisticsResponse struct {
	Income   float64          `json:"income"`
	Expenses float64          `json:"expenses"`
	Periods  []PeriodStats    `json:"periods"`
}

type PeriodStats struct {
	Label    string  `json:"label"`
	Income   float64 `json:"income"`
	Expenses float64 `json:"expenses"`
}

type ActivityLogResponse struct {
	Logs  []ActivityLog `json:"logs"`
	Total int64         `json:"total"`
}
