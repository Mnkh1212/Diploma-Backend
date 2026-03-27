# FinTrack — Ажлын бүртгэл (Work Log)

## Төслийн тодорхойлолт
**Сэдэв:** Хиймэл оюун ухаанд суурилсан хувийн санхүүгийн хяналт ба зөвлөмжийн систем
**Төрөл:** Их сургуулийн diploma ажил

---

## 2026-03-27 — Төслийн анхны тохиргоо (Initial Setup)

### Хийгдсэн ажлууд:

#### 1. Төслийн бүтэц үүсгэсэн
- `backend/` — Go backend project
- `frontend/` — React Native (Expo) project
- `docs/` — Баримт бичиг
- `docker-compose.yml` — Docker orchestration

#### 2. Backend (Go) бүтээсэн
- **Framework:** Gin (HTTP router) + GORM (ORM)
- **Authentication:** JWT token-based auth (bcrypt password hashing)
- **Database:** PostgreSQL 16 (auto-migration via GORM)

**Үүсгэсэн файлууд:**
- `cmd/server/main.go` — Entry point
- `internal/config/config.go` — Environment config loader
- `internal/database/database.go` — DB connection, migration, seed data
- `internal/models/models.go` — Бүх data model, DTO-ууд
- `internal/middleware/auth.go` — JWT auth middleware, CORS
- `internal/handlers/auth.go` — Register, Login, Profile
- `internal/handlers/transaction.go` — CRUD transactions
- `internal/handlers/dashboard.go` — Dashboard, Expenses summary, Statistics
- `internal/handlers/budget.go` — CRUD budgets
- `internal/handlers/ai_chat.go` — AI chat (financial advisor)
- `internal/handlers/account.go` — Accounts, Categories
- `internal/handlers/scheduled_payment.go` — Scheduled payments
- `internal/routes/routes.go` — API route definitions

**Database Models:**
- `User` — Хэрэглэгч
- `Account` — Дансны мэдээлэл (bank, cash, credit_card, investment)
- `Category` — Гүйлгээний ангилал (17 seed categories)
- `Transaction` — Орлого/Зарлага гүйлгээ
- `Budget` — Сарын төсөв
- `ScheduledPayment` — Давтагдах төлбөр
- `AIChat` — AI чатын session
- `AIMessage` — Чатын мессежүүд

#### 3. Docker тохиргоо
- `backend/Dockerfile` — Multi-stage Go build (alpine)
- `docker-compose.yml` — PostgreSQL 16 + Go backend
- PostgreSQL healthcheck тохируулсан
- Volume mount (persistent data)

#### 4. Frontend (React Native) бүтээсэн
- **Framework:** Expo (blank template)
- **Styling:** NativeWind (Tailwind CSS for React Native)
- **Navigation:** React Navigation (stack + bottom tabs)
- **HTTP Client:** Axios
- **State:** React Context (AuthContext)

**Үүсгэсэн дэлгэцүүд (Screens):**
- `OnboardingScreen` — Нэвтрэх/бүртгэлийн нүүр
- `LoginScreen` — Нэвтрэх
- `RegisterScreen` — Бүртгүүлэх
- `HomeScreen` — Dashboard (баланс, гүйлгээ, quick actions)
- `TransactionsScreen` — Гүйлгээний түүх (хайлт, шүүлт)
- `ExpensesScreen` — Зарлагын дүн шинжилгээ (donut chart)
- `BudgetScreen` — Сарын төсөв, bar chart
- `StatisticsScreen` — Статистик (income vs expenses)
- `AIChatScreen` — AI санхүүгийн зөвлөх чат
- `SettingsScreen` — Тохиргоо
- `AddTransactionScreen` — Шинэ гүйлгээ нэмэх

**Navigation бүтэц:**
- Auth Stack: Onboarding → Login / Register
- Main Bottom Tabs: Home | Analytics | (+)Add | AI Chat | Settings
- Modal: AddTransaction

**Дизайн:**
- Dark theme (Figma дизайн дагуу)
- Primary: #00C853 (green)
- Background: #0D0D0D
- Card: #1A1A2E
- Accent colors: orange, red, purple, blue, yellow

---

## Дараагийн алхамууд (Next Steps)
- [ ] Backend Docker build test
- [ ] Frontend-Backend integration test
- [ ] AI chat-д external API (Claude/OpenAI) холбох
- [ ] Notification system нэмэх
- [ ] Data export/import feature
- [ ] Unit test бичих
- [ ] Performance optimization
