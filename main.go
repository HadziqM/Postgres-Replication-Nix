package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/BurntSushi/toml"
	_ "github.com/lib/pq"
)

type Config struct {
	Master  Database   `toml:"master"`
	Replica []Database `toml:"replica"`
}

type Database struct {
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	User     string `toml:"user"`
	Password string `toml:"password"`
	Database string `toml:"database"`
}

type Chat struct {
	ID        int       `json:"id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type Server struct {
	db1 *sql.DB
	db2 *sql.DB
}

func main() {
	// Read config file
	var config Config
	if _, err := toml.DecodeFile("config.toml", &config); err != nil {
		log.Fatal("Failed to read config.toml:", err)
	}

	// Connect to master database
	db1Conn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Master.Host,
		config.Master.Port,
		config.Master.User,
		config.Master.Password,
		config.Master.Database,
	)

	db1, err := sql.Open("postgres", db1Conn)
	if err != nil {
		log.Fatal("Failed to connect to master DB:", err)
	}
	defer db1.Close()

	// Connect to replica database
	if len(config.Replica) == 0 {
		log.Fatal("No replica database configured")
	}

	replica := config.Replica[0]
	db2Conn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		replica.Host,
		replica.Port,
		config.Master.User, // Use master credentials if not specified
		config.Master.Password,
		config.Master.Database,
	)

	db2, err := sql.Open("postgres", db2Conn)
	if err != nil {
		log.Fatal("Failed to connect to replica DB:", err)
	}
	defer db2.Close()

	// Test connections
	if err := db1.Ping(); err != nil {
		log.Fatal("Master DB ping failed:", err)
	}
	if err := db2.Ping(); err != nil {
		log.Fatal("Replica DB ping failed:", err)
	}

	log.Println("Connected to both databases successfully!")
	log.Printf("Master: %s:%d", config.Master.Host, config.Master.Port)
	log.Printf("Replica: %s:%d", replica.Host, replica.Port)

	srv := &Server{db1: db1, db2: db2}

	// Routes
	http.HandleFunc("/", srv.handleHome)
	http.HandleFunc("/api/chats", srv.handleChats)
	http.HandleFunc("/api/compare", srv.handleCompare)

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	tmpl := `
<!DOCTYPE html>
<html>
<head>
	<title>PostgreSQL Replication Demo</title>
	<style>
		body { font-family: Arial, sans-serif; max-width: 1200px; margin: 0 auto; padding: 20px; }
		.container { display: flex; gap: 20px; margin-top: 20px; }
		.db-section { flex: 1; border: 1px solid #ddd; padding: 15px; border-radius: 5px; }
		.db-section h2 { margin-top: 0; }
		input, button { padding: 8px; margin: 5px 0; }
		button { background: #007bff; color: white; border: none; cursor: pointer; border-radius: 3px; }
		button:hover { background: #0056b3; }
		.chat-list { max-height: 400px; overflow-y: auto; margin-top: 10px; }
		.chat-item { background: #f8f9fa; padding: 10px; margin: 5px 0; border-radius: 3px; }
		.comparison { margin-top: 20px; padding: 15px; background: #e7f3ff; border-radius: 5px; }
		.match { color: green; font-weight: bold; }
		.mismatch { color: red; font-weight: bold; }
	</style>
</head>
<body>
	<h1>PostgreSQL Streaming Replication Demo</h1>
	<p>This demo shows data synchronization between two PostgreSQL databases.</p>
	
	<div>
		<h3>Add New Chat Message (to Master DB)</h3>
		<input type="text" id="message" placeholder="Enter message" style="width: 300px;">
		<button onclick="addChat()">Add to Master</button>
	</div>

	<div class="container">
		<div class="db-section">
			<h2>Master Database</h2>
			<button onclick="loadChats('master')">Refresh</button>
			<div id="master-chats" class="chat-list"></div>
		</div>
		<div class="db-section">
			<h2>Replica Database</h2>
			<button onclick="loadChats('replica')">Refresh</button>
			<div id="replica-chats" class="chat-list"></div>
		</div>
	</div>

	<div class="comparison">
		<h3>Replication Status</h3>
		<button onclick="compareDBs()">Compare Databases</button>
		<div id="comparison-result"></div>
	</div>

	<script>
		async function addChat() {
			const msg = document.getElementById('message').value;
			if (!msg) return alert('Please enter a message');
			
			const res = await fetch('/api/chats', {
				method: 'POST',
				headers: {'Content-Type': 'application/json'},
				body: JSON.stringify({message: msg})
			});
			
			if (res.ok) {
				document.getElementById('message').value = '';
				setTimeout(() => {
					loadChats('master');
					loadChats('replica');
				}, 100);
			}
		}

		async function loadChats(db) {
			const res = await fetch('/api/chats?db=' + db);
			const chats = await res.json();
			const container = document.getElementById(db + '-chats');
			
			container.innerHTML = chats.map(c => 
				'<div class="chat-item"><strong>#' + c.id + '</strong>: ' + c.message + 
				'<br><small>' + new Date(c.created_at).toLocaleString() + '</small></div>'
			).join('');
		}

		async function compareDBs() {
			const res = await fetch('/api/compare');
			const data = await res.json();
			const result = document.getElementById('comparison-result');
			
			result.innerHTML = '<p class="' + (data.match ? 'match' : 'mismatch') + '">' +
				(data.match ? '✓ Databases are in sync!' : '✗ Databases are NOT in sync') +
				'</p><p>Master: ' + data.count1 + ' records | Replica: ' + data.count2 + ' records</p>';
		}

		// Load initial data
		loadChats('master');
		loadChats('replica');
		compareDBs();
	</script>
</body>
</html>
	`
	w.Header().Set("Content-Type", "text/html")
	template.Must(template.New("home").Parse(tmpl)).Execute(w, nil)
}

func (s *Server) handleChats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case "GET":
		db := r.URL.Query().Get("db")
		var chats []Chat
		var dbConn *sql.DB

		if db == "replica" {
			dbConn = s.db2
		} else {
			dbConn = s.db1
		}

		rows, err := dbConn.Query("SELECT id, message, created_at FROM chat ORDER BY id DESC")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var c Chat
			if err := rows.Scan(&c.ID, &c.Message, &c.CreatedAt); err != nil {
				continue
			}
			chats = append(chats, c)
		}

		json.NewEncoder(w).Encode(chats)

	case "POST":
		var chat Chat
		if err := json.NewDecoder(r.Body).Decode(&chat); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err := s.db1.QueryRow(
			"INSERT INTO chat (message) VALUES ($1) RETURNING id, created_at",
			chat.Message,
		).Scan(&chat.ID, &chat.CreatedAt)

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(chat)
	}
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	var count1, count2 int

	s.db1.QueryRow("SELECT COUNT(*) FROM chat").Scan(&count1)
	s.db2.QueryRow("SELECT COUNT(*) FROM chat").Scan(&count2)

	result := map[string]interface{}{
		"count1": count1,
		"count2": count2,
		"match":  count1 == count2,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
