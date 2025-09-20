```
PROJECT SPECIFICATION: casvhpctl - Complete Full-System Web Hosting and VPS Control Panel

OVERVIEW:
casvhpctl is a single-binary, full-stack web hosting and VPS control panel written in Go with an embedded React/HTML frontend. It completely takes over and manages all system services (nginx, postfix, dovecot, bind, php-fpm, docker, libvirt) by nuking existing configurations and rebuilding them with embedded templates and sane defaults. The system is designed for enterprise use and self-hosting, supporting multiple Linux distributions with automatic detection and configuration.

LICENSE: GNU General Public License v3.0

PROGRAMMING LANGUAGE: Go 1.21+

BUILD TARGETS: Linux amd64, Linux arm64

CORE ARCHITECTURE PRINCIPLES:
1. Single Go binary with embedded frontend (no external dependencies)
2. SQLite as authoritative configuration database (WAL mode)
3. Complete system service takeover - nuke and rebuild all configs
4. TCP-based service communication (PHP-FPM uses TCP ports 64000-64999)
5. Embedded configuration templates for all services
6. Environment variable override for every setting
7. Mobile-first, responsive UI with dark/light/Dracula themes (Dracula default)
8. JWT-based authentication with scoped API tokens
9. User-isolated resources (containers, VMs, networks, quotas)
10. Auto-detection of system capabilities and network interfaces

SUPPORTED OPERATING SYSTEMS:
- Debian 10, 11, 12 (primary support)
- Ubuntu 18.04, 20.04, 22.04, 24.04 LTS
- AlmaLinux 8, 9
- RHEL 8, 9
- CentOS Stream 8, 9
- Rocky Linux 8, 9
- Arch Linux (rolling)
- Alpine Linux 3.15+
- openSUSE Leap 15.4+, Tumbleweed
- Linux Mint 20, 21
- ClearLinux (latest)
- NixOS 22.11+
- Void Linux (glibc)

PACKAGE MANAGER DETECTION AND MAPPING:
Automatically detect package manager and map common packages:
- apt (Debian/Ubuntu): nginx, postfix, dovecot-core, bind9, mariadb-server, postgresql, docker.io, fail2ban, ufw, certbot, php8.3-fpm
- yum/dnf (RHEL/CentOS/AlmaLinux): nginx, postfix, dovecot, bind, mariadb-server, postgresql-server, docker, fail2ban, firewalld, certbot, php-fpm
- pacman (Arch): nginx, postfix, dovecot, bind, mariadb, postgresql, docker, fail2ban, ufw, certbot, php-fpm
- apk (Alpine): nginx, postfix, dovecot, bind, mariadb, postgresql, docker, fail2ban, iptables, certbot, php83-fpm
- zypper (openSUSE): nginx, postfix, dovecot, bind, mariadb, postgresql, docker, fail2ban, firewalld, certbot, php8-fpm

COMPLETE FEATURE SET:

1. WEB HOSTING MANAGEMENT:
- Multi-domain virtual hosts with automatic nginx configuration generation
- Multi-PHP version support (7.4, 8.0, 8.1, 8.2, 8.3) with TCP-based PHP-FPM pools
- Random port assignment (64000-64999 range) for each PHP version
- Apache backend integration for legacy PHP applications
- Automatic SSL certificate management via Let's Encrypt with internal CA fallback
- FTP/SFTP access configuration per virtual host
- Custom nginx configuration snippets per domain
- Document root auto-creation with default landing pages
- Redirect management (301/302) with domain mapping
- .well-known directory handling for SSL validation
- Security headers and rate limiting configuration
- Static file caching and gzip compression
- Custom error pages and maintenance mode

2. DNS MANAGEMENT (BIND):
- Complete BIND server takeover with custom configuration
- Primary, secondary, forward, and dynamic zone support
- Automatic zone file generation from database records
- Support for all DNS record types: A, AAAA, CNAME, MX, TXT, PTR, SRV, NS
- Zone file backup and versioning
- DNSSEC support and configuration
- DNS forwarder configuration with multiple upstream servers
- Split-horizon DNS support
- Dynamic DNS updates via API
- Bulk DNS operations and zone imports
- External DNS provider integration (Namecheap, Porkbun, Cloudflare)
- Automatic reverse DNS zone creation
- DNS query logging and statistics

3. EMAIL STACK MANAGEMENT:
- Complete Postfix configuration with virtual domain support
- Dovecot IMAP/POP3 server with maildir storage
- SMTP submission on ports 25, 465, 587 with TLS
- SASL authentication integration between Postfix and Dovecot
- Virtual mailbox and alias management via SQLite
- Anti-spam integration: SpamAssassin, ClamAV, OpenDKIM, OpenDMARC
- Sieve mail filtering with web interface
- Email account quota management
- Catch-all and forward-all configurations
- Random alias generation (privacy-focused like DuckDuckGo Email)
- Email encryption support (GPG integration)
- Mailing list management
- Email backup and archiving
- SMTP relay configuration for external providers
- Email template system for notifications

4. DATABASE MANAGEMENT:
- MariaDB/MySQL server configuration and management
- PostgreSQL server setup and administration
- MongoDB deployment and configuration
- Valkey (Redis fork) for caching and sessions
- Per-user database isolation and access control
- Automatic database backup scheduling
- Connection string generation and management
- Database user privilege management
- Query optimization and performance monitoring
- Embedded phpMyAdmin, phpPgAdmin, and MongoDB admin interfaces
- Database replication setup for high availability
- SQL injection protection and security hardening

5. CONTAINER ORCHESTRATION:
- Docker and Podman support with automatic detection
- Firecracker and Kata container runtime support
- User-isolated container networks with automatic bridge creation
- Custom OCI registry integration (public and private)
- Container image management and caching
- Port mapping and volume management
- Environment variable injection and secrets management
- Container health monitoring and auto-restart policies
- Docker Compose file import and management
- Container backup and migration tools
- Resource limits (CPU, memory, disk) per container
- Container log aggregation and analysis
- Multi-architecture image support (amd64, arm64)

6. VIRTUAL MACHINE MANAGEMENT:
- libvirt/KVM integration with nested virtualization detection
- Incus (LXD) container and VM support
- Automatic ISO download for major Linux distributions
- VM template creation and cloning
- Snapshot management and rollback functionality
- VNC/SPICE console access via web interface
- VM migration between hosts
- Resource allocation (CPU, RAM, disk) per VM
- Network bridge configuration with DHCP
- VM backup and disaster recovery
- Cloud-init integration for automated provisioning
- VM performance monitoring and optimization
- Storage pool management (local, NFS, Ceph)

7. SECURITY AND FIREWALL:
- Complete iptables/nftables/ufw/firewalld management
- fail2ban integration with user-configurable whitelists
- Automatic intrusion detection and prevention
- SSL/TLS certificate management with automatic renewal
- Internal Certificate Authority for development environments
- 2FA/WebAuthn support for enhanced authentication
- Scoped API token system (global, admin, readonly, user)
- Emergency SSH access on randomized high ports (not 2222)
- Security audit logging and compliance reporting
- Vulnerability scanning and patch management
- Rate limiting and DDoS protection
- Secure password policies and enforcement
- Session management and timeout configuration

8. USER MANAGEMENT AND ACCESS CONTROL:
- Multi-tenant user system with role-based access control
- Roles: global_admin, admin, user with granular permissions
- First user automatically becomes global_admin
- User quota management (disk, bandwidth, resources)
- User-scoped resource isolation
- Registration system with email verification
- Password reset and account recovery
- User activity logging and audit trails
- Bulk user operations and CSV import/export
- LDAP/Active Directory integration support
- Single Sign-On (SSO) with SAML/OAuth2
- User profile management and preferences
- Account suspension and termination workflows

9. BILLING AND SUBSCRIPTION MANAGEMENT:
- Flexible plan system: Free, Bronze, Silver, Gold tiers
- Trial period support with automatic conversion
- Resource quota enforcement per plan
- Payment provider integration (Stripe, PayPal)
- Invoice generation and automated billing
- Usage tracking and metering
- Proration and plan changes
- Payment failure handling and dunning
- Tax calculation and compliance
- Subscription analytics and reporting
- Coupon and discount code system
- White-label billing customization

10. BACKUP AND DISASTER RECOVERY:
- Automated backup scheduling for all services
- S3-compatible storage backend support
- Encrypted backup vaults with AES-256
- Point-in-time recovery for databases
- Full system backup and bare-metal restore
- Incremental and differential backup strategies
- Backup verification and integrity checking
- Off-site backup replication
- Disaster recovery planning and testing
- Backup retention policies and cleanup
- Restore testing and validation
- Emergency recovery procedures

11. MONITORING AND ALERTING:
- Built-in monitoring dashboard (Uptime Kuma style)
- System resource monitoring (CPU, RAM, disk, network)
- Service health checks and status pages
- Log aggregation and analysis
- Performance metrics collection
- Alert configuration via email, SMS, webhooks
- Threshold-based alerting with escalation
- Integration with external monitoring (Prometheus, Grafana)
- Custom monitoring checks and scripts
- Historical data retention and trending
- Capacity planning and forecasting
- SLA monitoring and reporting

12. SUPPORT SYSTEM:
- Integrated ticketing system with priority levels
- Live chat support with session management
- Knowledge base and documentation system
- Support email integration (support@domain → tickets)
- Ticket escalation and assignment workflows
- Customer satisfaction surveys
- Support analytics and reporting
- Multi-language support interface
- Screen sharing and remote assistance tools
- Support ticket API for external integrations

13. API AND AUTOMATION:
- RESTful API with OpenAPI 3.0 specification
- Webhook support for event notifications
- CLI tool for server management and automation
- Scheduled task management with cron-like syntax
- Custom script execution and management
- Infrastructure as Code (IaC) support
- Integration with popular DevOps tools
- Rate limiting and API key management
- API versioning and backward compatibility
- Bulk operations and batch processing
- Real-time WebSocket connections for live updates

NETWORK CONFIGURATION:
- Automatic WAN interface detection (excludes loopback, docker0, br-)
- Internal network allocation (default: 10.100.0.0/16)
- IPv6 support with automatic configuration
- Per-user isolated networks and VLANs
- DHCP server configuration for VM/container networks
- NAT and port forwarding management
- Load balancer integration (HAProxy, nginx)
- CDN integration and configuration
- Bandwidth monitoring and traffic shaping

EMBEDDED CONFIGURATION TEMPLATES:

NGINX MAIN CONFIGURATION:
- Worker process auto-detection
- Security headers and SSL configuration
- Rate limiting zones for API and login endpoints
- Gzip compression and caching rules
- Default server blocks for control panel
- Virtual host inclusion directory
- Log format and rotation configuration
- Client upload limits and timeouts

PHP-FPM CONFIGURATION:
- TCP-based pools on random ports (64000-64999)
- Security restrictions (disable dangerous functions)
- Performance tuning (memory limits, execution time)
- Process management (dynamic with auto-scaling)
- Error logging and debugging configuration
- Per-version pool isolation

POSTFIX CONFIGURATION:
- Virtual domain and mailbox support
- TLS encryption with modern ciphers
- SASL authentication integration
- Anti-spam and anti-virus integration
- Message size and queue management
- SQLite database queries for virtual users
- Relay configuration for external providers

DOVECOT CONFIGURATION:
- Single-file configuration (no includes)
- IMAP/POP3/LMTP protocol support
- Maildir storage with automatic namespace creation
- SQL authentication against SQLite database
- Sieve mail filtering support
- SSL/TLS configuration with modern ciphers
- Quota enforcement and management

BIND DNS CONFIGURATION:
- Authoritative and recursive DNS support
- Zone file management and generation
- DNSSEC configuration and key management
- Query logging and statistics
- Forwarder configuration with fallback
- Security and performance optimizations

SYSTEM INITIALIZATION PROCESS:
1. Detect operating system and package manager
2. Install required system packages automatically
3. Stop all conflicting services
4. Backup existing configurations to timestamped directory
5. Create system users (vmail:5000, www-data)
6. Generate random ports for PHP-FPM services
7. Create directory structure (/var/lib/casvhpctl, /etc/casvhpctl)
8. Initialize SQLite database with complete schema
9. Generate self-signed certificates for initial setup
10. Deploy embedded configuration templates
11. Configure firewall rules for all services
12. Start and enable all managed services
13. Create default admin user and display credentials
14. Verify all services are running correctly

DIRECTORY STRUCTURE:
/etc/casvhpctl/
├── ssl/                    # SSL certificates and keys
├── configs/               # Backup of original configurations
└── casvhpctl.conf         # Main configuration file

/var/lib/casvhpctl/
├── casvhpctl.db          # SQLite database
├── backups/              # System backups
├── logs/                 # Application logs
└── uploads/              # User file uploads

/var/www/
├── casvhpctl/            # Control panel landing page
└── vhosts/               # User website document roots

ENVIRONMENT VARIABLES (ALL CONFIGURABLE):
CASVHP_HOST=0.0.0.0
CASVHP_PORT=8080
CASVHP_UI_PORT=10443
CASVHP_TLS_ENABLED=false
CASVHP_DATABASE_PATH=/var/lib/casvhpctl/casvhpctl.db
CASVHP_JWT_SECRET=(auto-generated)
CASVHP_SESSION_TIMEOUT=480
CASVHP_EMERGENCY_SSH_PORT=2849
CASVHP_ENABLE_REGISTRATION=true
CASVHP_ENABLE_BILLING=true
CASVHP_ENABLE_VIRTUALIZATION=true
CASVHP_ENABLE_CONTAINERS=true
CASVHP_ENABLE_EMAIL=true
CASVHP_ENABLE_DNS=true
CASVHP_BRAND_NAME=casvhpctl
CASVHP_DEFAULT_THEME=dracula
CASVHP_WAN_INTERFACE=auto
CASVHP_INTERNAL_NETWORK=10.100.0.0/16
CASVHP_ENABLE_IPV6=true
CASVHP_PHP_VERSIONS=7.4,8.0,8.1,8.2,8.3
CASVHP_DEFAULT_PHP_VERSION=8.3
CASVHP_SSL_PROVIDER=letsencrypt
CASVHP_DNS_FORWARDERS=8.8.8.8,8.8.4.4,1.1.1.1
CASVHP_BACKUP_RETENTION_DAYS=30
CASVHP_LOG_RETENTION_DAYS=90
CASVHP_QUOTA_DEFAULT=1073741824
CASVHP_FIREWALL_BACKEND=auto
CASVHP_CONTAINER_RUNTIME=docker
CASVHP_VIRTUALIZATION_BACKEND=libvirt

DATABASE SCHEMA (COMPLETE):
22 tables with full relationships, indexes, and constraints:
- users (authentication and profiles)
- tokens (API keys and app passwords)
- vhosts (virtual hosts and websites)
- dns_zones (DNS zone configuration)
- dns_records (individual DNS records)
- mail_domains (email domain configuration)
- mail_accounts (email user accounts)
- mail_aliases (email aliases and forwards)
- databases (database instances)
- containers (container configurations)
- virtual_machines (VM configurations)
- backups (backup job tracking)
- system_logs (comprehensive logging)
- login_history (authentication audit)
- support_tickets (customer support)
- support_ticket_messages (ticket conversations)
- billing_plans (subscription tiers)
- user_subscriptions (user billing)
- config_store (system configuration)
- firewall_rules (security rules)
- scheduled_tasks (automation)
- All tables include proper foreign keys, indexes, and audit timestamps

FRONTEND INTERFACE:
- Embedded React/HTML5 application with modern JavaScript
- Mobile-first responsive design with CSS Grid and Flexbox
- Dark mode (Dracula theme) as default with light mode option
- Real-time updates via WebSocket connections
- Progressive Web App (PWA) capabilities
- Offline functionality for basic operations
- Accessibility compliance (WCAG 2.1 AA)
- Multi-language support with i18n
- Setup wizards for all major features
- Dashboard with system overview and quick actions
- Tabbed interface with searchable navigation
- Form validation with real-time feedback
- File upload with drag-and-drop support
- Contextual help system with tutorials

SECURITY IMPLEMENTATION:
- JWT tokens with configurable expiration
- bcrypt password hashing with salt rounds
- Rate limiting on authentication endpoints
- CORS configuration for API access
- SQL injection prevention with prepared statements
- XSS protection with content security policy
- CSRF protection with secure tokens
- Secure session management
- Input validation and sanitization
- Error handling without information disclosure
- Audit logging for all operations
- Regular security updates and patches

BUILD SYSTEM (MAKEFILE):
- Cross-platform compilation for Linux amd64/arm64
- Embedded frontend build process
- Dependency management and validation
- Unit and integration testing
- Code coverage reporting
- Static analysis and linting
- Security vulnerability scanning
- Documentation generation
- Release packaging and signing
- Installation and uninstallation scripts
- Service management integration
- Development environment setup

INSTALLATION METHODS:
1. Binary download with automatic installer
2. Package manager installation (deb, rpm, apk)
3. Docker container deployment
4. Source compilation with make install
5. Cloud marketplace images (AWS, DigitalOcean, etc.)

ERROR HANDLING AND RECOVERY:
- Graceful degradation of services
- Automatic service restart on failure
- Configuration validation and rollback
- Database transaction safety
- Backup restoration procedures
- Emergency mode operation
- Detailed error reporting and logging
- Self-healing capabilities where possible

PERFORMANCE OPTIMIZATIONS:
- Database connection pooling
- Query optimization and indexing
- Memory usage monitoring and limits
- CPU usage throttling for background tasks
- Disk I/O optimization
- Network bandwidth management
- Caching strategies for frequent operations
- Lazy loading for large datasets

COMPLIANCE AND STANDARDS:
- GDPR compliance for user data
- SOC 2 Type II controls
- ISO 27001 security standards
- PCI DSS for payment processing
- HIPAA compatibility options
- Regular compliance audits
- Data retention policies
- Privacy controls and consent management

The final implementation will be a complete, production-ready hosting control panel that can compete with commercial solutions like cPanel/WHM and Plesk while remaining open-source and self-hostable. Every component will be thoroughly tested, documented, and ready for enterprise deployment.
```

