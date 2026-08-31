pipeline {
    agent none

    triggers {
        // Daily build at 3am UTC (matches GitHub Actions daily.yml)
        cron('0 3 * * *')
        // Devel image build at 4am UTC (matches GitHub Actions docker.yml build-devel job)
        cron('0 4 * * *')
    }

    environment {
        PROJECT_NAME = 'cashp'
        PROJECT_ORG = 'webappsgo'
        BINDIR = 'binaries'
        RELDIR = 'releases'
        // Go cache bind-mounted from host: GO_CACHE (mod) and GO_BUILD (build cache)

        // =========================================================================
        // GIT PROVIDER CONFIGURATION
        // Uncomment ONE block below based on your git hosting platform
        // =========================================================================

        // ----- GITHUB (default) -----
        GIT_FQDN = 'github.com'
        // Jenkins credentials ID
        GIT_TOKEN = credentials('github-token')
        REGISTRY = "ghcr.io/${PROJECT_ORG}/${PROJECT_NAME}"

        // ----- GITEA / FORGEJO (self-hosted) -----
        // GIT_FQDN = 'git.example.com'  // Your Gitea/Forgejo domain
        // GIT_TOKEN = credentials('gitea-token')  // Jenkins credentials ID
        // REGISTRY = "${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}"

        // ----- GITLAB (gitlab.com or self-hosted) -----
        // GIT_FQDN = 'gitlab.com'  // or your self-hosted GitLab domain
        // GIT_TOKEN = credentials('gitlab-token')  // Jenkins credentials ID
        // REGISTRY = "registry.${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}"

        // ----- DOCKER HUB -----
        // GIT_FQDN = 'github.com'  // Git provider (separate from registry)
        // GIT_TOKEN = credentials('github-token')
        // REGISTRY = "docker.io/${PROJECT_ORG}/${PROJECT_NAME}"

        // =========================================================================
    }

    stages {
        stage('Setup') {
            agent { label 'amd64' }
            steps {
                script {
                    // Determine build type and version
                    if (env.TAG_NAME) {
                        // Release build (tag push) - matches release.yml
                        env.BUILD_TYPE = 'release'
                        env.VERSION = env.TAG_NAME.replaceFirst('^v', '')
                    } else if (env.BRANCH_NAME == 'beta') {
                        // Beta build - matches beta.yml
                        env.BUILD_TYPE = 'beta'
                        env.VERSION = sh(script: 'date -u +"%Y%m%d%H%M%S"', returnStdout: true).trim() + '-beta'
                    } else if (env.BRANCH_NAME == 'main' || env.BRANCH_NAME == 'master') {
                        // Daily build - matches daily.yml
                        // Daily identity is the commit, not a timestamp - the daily
                        // release is a single rolling release, not a versioned tag
                        env.BUILD_TYPE = 'daily'
                        env.VERSION = sh(script: 'git rev-parse --short=7 HEAD', returnStdout: true).trim()
                    } else {
                        // Other branches - dev build
                        env.BUILD_TYPE = 'dev'
                        env.VERSION = sh(script: 'date -u +"%Y%m%d%H%M%S"', returnStdout: true).trim() + '-dev'
                    }
                    env.COMMIT_ID = sh(script: 'git rev-parse --short=7 HEAD', returnStdout: true).trim()
                    env.BUILD_EPOCH = sh(script: 'date -u +%s', returnStdout: true).trim()
                    env.BUILD_DATE = sh(script: "date -u -d @${env.BUILD_EPOCH} +\"%Y-%m-%dT%H:%M:%SZ\"", returnStdout: true).trim()
                    // OFFICIAL_SITE (optional): site.txt wins; otherwise use Jenkins credentials or leave empty
                    // Never guess or assume - must be explicitly defined by user
                    env.OFFICIAL_SITE = sh(script: '[ -f site.txt ] && cat site.txt || echo "${OFFICIAL_SITE:-}"', returnStdout: true).trim()
                    env.LDFLAGS = "-s -w -X 'main.Version=${env.VERSION}' -X 'main.CommitID=${env.COMMIT_ID}' -X 'main.BuildEpoch=${env.BUILD_EPOCH}' -X 'main.OfficialSite=${env.OFFICIAL_SITE}'"
                    env.HAS_CLI = sh(script: '[ -d src/client ] && echo true || echo false', returnStdout: true).trim()
                    env.HAS_AGENT = sh(script: '[ -d src/agent ] && echo true || echo false', returnStdout: true).trim()
                }
                sh 'mkdir -p ${BINDIR} ${RELDIR}'
                echo "Build type: ${BUILD_TYPE}, Version: ${VERSION}"
            }
        }

        stage('Build Server') {
            parallel {
                // Linux
                stage('Linux AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-linux-amd64 ./src
                        '''
                    }
                }
                stage('Linux ARM64') {
                    agent { label 'arm64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-linux-arm64 ./src
                        '''
                    }
                }
                // Darwin (macOS)
                stage('Darwin AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-darwin-amd64 ./src
                        '''
                    }
                }
                stage('Darwin ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-darwin-arm64 ./src
                        '''
                    }
                }
                // Windows
                stage('Windows AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-windows-amd64.exe ./src
                        '''
                    }
                }
                stage('Windows ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-windows-arm64.exe ./src
                        '''
                    }
                }
                // FreeBSD
                stage('FreeBSD AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-freebsd-amd64 ./src
                        '''
                    }
                }
                stage('FreeBSD ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-freebsd-arm64 ./src
                        '''
                    }
                }
            }
        }

        // CLI builds - only if src/client/ exists (matches GitHub Actions)
        stage('Build CLI') {
            when {
                expression { env.HAS_CLI == 'true' }
            }
            parallel {
                stage('CLI Linux AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-linux-amd64 ./src/client
                        '''
                    }
                }
                stage('CLI Linux ARM64') {
                    agent { label 'arm64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-linux-arm64 ./src/client
                        '''
                    }
                }
                stage('CLI Darwin AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-darwin-amd64 ./src/client
                        '''
                    }
                }
                stage('CLI Darwin ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-darwin-arm64 ./src/client
                        '''
                    }
                }
                stage('CLI Windows AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-windows-amd64.exe ./src/client
                        '''
                    }
                }
                stage('CLI Windows ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-windows-arm64.exe ./src/client
                        '''
                    }
                }
                stage('CLI FreeBSD AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-freebsd-amd64 ./src/client
                        '''
                    }
                }
                stage('CLI FreeBSD ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-cli-freebsd-arm64 ./src/client
                        '''
                    }
                }
            }
        }

        // Agent builds - only if src/agent/ exists (matches GitHub Actions)
        stage('Build Agent') {
            when {
                expression { env.HAS_AGENT == 'true' }
            }
            parallel {
                stage('Agent Linux AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-agent-linux-amd64 ./src/agent
                        '''
                    }
                }
                stage('Agent Linux ARM64') {
                    agent { label 'arm64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=linux \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-agent-linux-arm64 ./src/agent
                        '''
                    }
                }
                stage('Agent Darwin AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-agent-darwin-amd64 ./src/agent
                        '''
                    }
                }
                stage('Agent Darwin ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=darwin \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-agent-darwin-arm64 ./src/agent
                        '''
                    }
                }
                stage('Agent Windows AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-agent-windows-amd64.exe ./src/agent
                        '''
                    }
                }
                stage('Agent Windows ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=windows \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-agent-windows-arm64.exe ./src/agent
                        '''
                    }
                }
                stage('Agent FreeBSD AMD64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=amd64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-agent-freebsd-amd64 ./src/agent
                        '''
                    }
                }
                stage('Agent FreeBSD ARM64') {
                    agent { label 'amd64' }
                    steps {
                        sh '''
                            docker run --rm \
                                --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                                -v ${WORKSPACE}:/app \
                                -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                                -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                                -w /app \
                                -e CGO_ENABLED=0 \
                                -e GOOS=freebsd \
                                -e GOARCH=arm64 \
                                casjaysdev/go:latest \
                                go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o ${BINDIR}/${PROJECT_NAME}-agent-freebsd-arm64 ./src/agent
                        '''
                    }
                }
            }
        }

        stage('Test') {
            agent { label 'amd64' }
            steps {
                sh '''
                    docker run --rm \
                        --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                        -v ${WORKSPACE}:/app \
                        -v ${GO_CACHE:-$HOME/go/pkg/mod}:/usr/local/share/go/pkg/mod \
                        -v ${GO_BUILD:-$HOME/.cache/go-build/${PROJECT_NAME}}:/usr/local/share/go/cache \
                        -w /app \
                        casjaysdev/go:latest \
                        go test -v -cover ./...
                '''
            }
        }

        // Stable Release - matches release.yml (tag push only)
        stage('Release: Stable') {
            agent { label 'amd64' }
            when {
                expression { env.BUILD_TYPE == 'release' }
            }
            steps {
                sh '''
                    echo "${VERSION}" > ${RELDIR}/version.txt

                    for f in ${BINDIR}/${PROJECT_NAME}-*; do
                        [ -f "$f" ] || continue
                        cp "$f" ${RELDIR}/
                    done

                    tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
                        --exclude='.forgejo' --exclude='binaries' --exclude='releases' \
                        --exclude='*.tar.gz' \
                        -czf ${RELDIR}/${PROJECT_NAME}-${VERSION}-source.tar.gz .
                '''
                sh '''
                    docker run --rm \
                        --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                        -v ${WORKSPACE}:/app \
                        -w /app \
                        casjaysdev/go:latest \
                        cyclonedx-gomod mod -json -output ${RELDIR}/${PROJECT_NAME}-sbom.cdx.json
                '''
                sh '''
                    cd ${RELDIR}
                    FILES="$(ls)"
                    sha256sum $FILES > sha256.txt
                    sha512sum $FILES > sha512.txt
                '''
                archiveArtifacts artifacts: 'releases/*', fingerprint: true
            }
        }

        // Beta Release - matches beta.yml (beta branch only)
        stage('Release: Beta') {
            agent { label 'amd64' }
            when {
                expression { env.BUILD_TYPE == 'beta' }
            }
            steps {
                sh '''
                    echo "${VERSION}" > ${RELDIR}/version.txt

                    for f in ${BINDIR}/${PROJECT_NAME}-*; do
                        [ -f "$f" ] || continue
                        cp "$f" ${RELDIR}/
                    done

                    tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
                        --exclude='.forgejo' --exclude='binaries' --exclude='releases' \
                        --exclude='*.tar.gz' \
                        -czf ${RELDIR}/${PROJECT_NAME}-${VERSION}-source.tar.gz .
                '''
                sh '''
                    docker run --rm \
                        --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                        -v ${WORKSPACE}:/app \
                        -w /app \
                        casjaysdev/go:latest \
                        cyclonedx-gomod mod -json -output ${RELDIR}/${PROJECT_NAME}-sbom.cdx.json
                '''
                sh '''
                    cd ${RELDIR}
                    FILES="$(ls)"
                    sha256sum $FILES > sha256.txt
                    sha512sum $FILES > sha512.txt
                '''
                archiveArtifacts artifacts: 'releases/*', fingerprint: true
            }
        }

        // Daily Release - matches daily.yml (main/master + scheduled)
        stage('Release: Daily') {
            agent { label 'amd64' }
            when {
                expression { env.BUILD_TYPE == 'daily' }
            }
            steps {
                sh '''
                    echo "${VERSION}" > ${RELDIR}/version.txt

                    for f in ${BINDIR}/${PROJECT_NAME}-*; do
                        [ -f "$f" ] || continue
                        cp "$f" ${RELDIR}/
                    done

                    tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
                        --exclude='.forgejo' --exclude='binaries' --exclude='releases' \
                        --exclude='*.tar.gz' \
                        -czf ${RELDIR}/${PROJECT_NAME}-${VERSION}-source.tar.gz .
                '''
                sh '''
                    docker run --rm \
                        --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
                        -v ${WORKSPACE}:/app \
                        -w /app \
                        casjaysdev/go:latest \
                        cyclonedx-gomod mod -json -output ${RELDIR}/${PROJECT_NAME}-sbom.cdx.json
                '''
                sh '''
                    cd ${RELDIR}
                    FILES="$(ls)"
                    sha256sum $FILES > sha256.txt
                    sha512sum $FILES > sha512.txt
                '''
                archiveArtifacts artifacts: 'releases/*', fingerprint: true
            }
        }

        // Docker - matches docker.yml (ALL branches and tags)
        stage('Docker') {
            agent { label 'amd64' }
            steps {
                script {
                    def tags = "-t ${REGISTRY}:${COMMIT_ID}"

                    if (env.BUILD_TYPE == 'release') {
                        // Release tag - version, latest, YYMM
                        def yymm = new Date().format('yyMM')
                        tags += " -t ${REGISTRY}:${VERSION}"
                        tags += " -t ${REGISTRY}:latest"
                        tags += " -t ${REGISTRY}:${yymm}"
                    } else if (env.BUILD_TYPE == 'beta') {
                        // Beta branch - beta
                        tags += " -t ${REGISTRY}:beta"
                    }
                    // All other branches - {commit_id} only (no :devel tag — see Docker Devel stage)

                    // Login to container registry
                    // Works with: ghcr.io, registry.gitlab.com, gitea/forgejo, docker.io
                    sh """
                        echo "\${GIT_TOKEN}" | docker login ${REGISTRY.split('/')[0]} -u ${PROJECT_ORG} --password-stdin
                    """

                    // Build multi-arch with OCI labels and manifest annotations
                    sh """
                        docker buildx create --name ${PROJECT_NAME}-builder --use 2>/dev/null || docker buildx use ${PROJECT_NAME}-builder
                        docker buildx build \
                            -f docker/Dockerfile \
                            --platform linux/amd64,linux/arm64 \
                            --build-arg VERSION="${VERSION}" \
                            --build-arg COMMIT_ID="${COMMIT_ID}" \
                            --build-arg BUILD_DATE="${BUILD_DATE}" \
                            --build-arg BUILD_EPOCH="${BUILD_EPOCH}" \
                            --build-arg OFFICIAL_SITE="${OFFICIAL_SITE}" \
                            --label "org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.title=${PROJECT_NAME}" \
                            --label "org.opencontainers.image.base.name=${PROJECT_NAME}" \
                            --label "org.opencontainers.image.description=${PROJECT_NAME} - standard image (alpine)" \
                            --label "org.opencontainers.image.licenses=MIT" \
                            --label "org.opencontainers.image.version=${VERSION}" \
                            --label "org.opencontainers.image.created=${BUILD_DATE}" \
                            --label "org.opencontainers.image.revision=${COMMIT_ID}" \
                            --label "org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --label "org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --label "org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.title=${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.base.name=${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.description=${PROJECT_NAME} - standard image (alpine)" \
                            --annotation "manifest:org.opencontainers.image.licenses=MIT" \
                            --annotation "manifest:org.opencontainers.image.version=${VERSION}" \
                            --annotation "manifest:org.opencontainers.image.created=${BUILD_DATE}" \
                            --annotation "manifest:org.opencontainers.image.revision=${COMMIT_ID}" \
                            --annotation "manifest:org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            ${tags} \
                            --push \
                            .
                    """
                }
            }
        }

        // Docker All-in-One - matches docker.yml build-aio (ALL branches and tags)
        // AIO uses same repo with -aio tag suffix: {repo}:{tag}-aio
        stage('Docker AIO') {
            agent { label 'amd64' }
            steps {
                script {
                    def tags = "-t ${REGISTRY}:${COMMIT_ID}-aio"

                    if (env.BUILD_TYPE == 'release') {
                        // Release tag - version, latest, YYMM
                        def yymm = new Date().format('yyMM')
                        tags += " -t ${REGISTRY}:${VERSION}-aio"
                        tags += " -t ${REGISTRY}:latest-aio"
                        tags += " -t ${REGISTRY}:${yymm}-aio"
                    } else if (env.BUILD_TYPE == 'beta') {
                        // Beta branch - beta-aio
                        tags += " -t ${REGISTRY}:beta-aio"
                    }
                    // All other branches - {commit_id}-aio only (no :devel-aio tag — Dockerfile.dev.aio does not exist)

                    // Login to container registry
                    sh """
                        echo "\${GIT_TOKEN}" | docker login ${REGISTRY.split('/')[0]} -u ${PROJECT_ORG} --password-stdin
                    """

                    // Build multi-arch all-in-one with OCI labels and manifest annotations
                    sh """
                        docker buildx create --name ${PROJECT_NAME}-builder --use 2>/dev/null || docker buildx use ${PROJECT_NAME}-builder
                        docker buildx build \
                            -f docker/Dockerfile.aio \
                            --platform linux/amd64,linux/arm64 \
                            --build-arg VERSION="${VERSION}" \
                            --build-arg COMMIT_ID="${COMMIT_ID}" \
                            --build-arg BUILD_DATE="${BUILD_DATE}" \
                            --build-arg BUILD_EPOCH="${BUILD_EPOCH}" \
                            --build-arg OFFICIAL_SITE="${OFFICIAL_SITE}" \
                            --label "org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.title=${PROJECT_NAME}-aio" \
                            --label "org.opencontainers.image.description=${PROJECT_NAME} - all-in-one (debian + postgresql + valkey + tor)" \
                            --label "org.opencontainers.image.licenses=MIT" \
                            --label "org.opencontainers.image.version=${VERSION}" \
                            --label "org.opencontainers.image.created=${BUILD_DATE}" \
                            --label "org.opencontainers.image.revision=${COMMIT_ID}" \
                            --label "org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --label "org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --label "org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.title=${PROJECT_NAME}-aio" \
                            --annotation "manifest:org.opencontainers.image.description=${PROJECT_NAME} - all-in-one (debian + postgresql + valkey + tor)" \
                            --annotation "manifest:org.opencontainers.image.licenses=MIT" \
                            --annotation "manifest:org.opencontainers.image.version=${VERSION}" \
                            --annotation "manifest:org.opencontainers.image.created=${BUILD_DATE}" \
                            --annotation "manifest:org.opencontainers.image.revision=${COMMIT_ID}" \
                            --annotation "manifest:org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            ${tags} \
                            --push \
                            .
                    """
                }
            }
        }

        // Docker Devel - matches docker.yml build-devel job (ALL branches, excl. tags, + daily 4am cron)
        // Dedicated stage, never folded into Docker/Docker AIO — no :devel-aio (Dockerfile.dev.aio does not exist)
        stage('Docker Devel') {
            agent { label 'amd64' }
            when {
                not { buildingTag() }
            }
            steps {
                script {
                    // Login to container registry
                    sh """
                        echo "\${GIT_TOKEN}" | docker login ${REGISTRY.split('/')[0]} -u ${PROJECT_ORG} --password-stdin
                    """

                    // Build multi-arch devel image with OCI labels and manifest annotations
                    sh """
                        docker buildx create --name ${PROJECT_NAME}-builder --use 2>/dev/null || docker buildx use ${PROJECT_NAME}-builder
                        docker buildx build \
                            -f docker/Dockerfile.dev \
                            --platform linux/amd64,linux/arm64 \
                            --build-arg VERSION="devel" \
                            --build-arg COMMIT_ID="${COMMIT_ID}" \
                            --build-arg BUILD_DATE="${BUILD_DATE}" \
                            --build-arg BUILD_EPOCH="${BUILD_EPOCH}" \
                            --build-arg OFFICIAL_SITE="${OFFICIAL_SITE}" \
                            --label "org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --label "org.opencontainers.image.title=${PROJECT_NAME}-devel" \
                            --label "org.opencontainers.image.base.name=${PROJECT_NAME}" \
                            --label "org.opencontainers.image.description=${PROJECT_NAME} - development image (alpine, debug mode)" \
                            --label "org.opencontainers.image.licenses=MIT" \
                            --label "org.opencontainers.image.version=devel" \
                            --label "org.opencontainers.image.created=${BUILD_DATE}" \
                            --label "org.opencontainers.image.revision=${COMMIT_ID}" \
                            --label "org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --label "org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --label "org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.vendor=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.authors=${PROJECT_ORG}" \
                            --annotation "manifest:org.opencontainers.image.title=${PROJECT_NAME}-devel" \
                            --annotation "manifest:org.opencontainers.image.base.name=${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.description=${PROJECT_NAME} - development image (alpine, debug mode)" \
                            --annotation "manifest:org.opencontainers.image.licenses=MIT" \
                            --annotation "manifest:org.opencontainers.image.version=devel" \
                            --annotation "manifest:org.opencontainers.image.created=${BUILD_DATE}" \
                            --annotation "manifest:org.opencontainers.image.revision=${COMMIT_ID}" \
                            --annotation "manifest:org.opencontainers.image.url=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.source=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            --annotation "manifest:org.opencontainers.image.documentation=https://${GIT_FQDN}/${PROJECT_ORG}/${PROJECT_NAME}" \
                            -t ${REGISTRY}:devel \
                            --push \
                            .
                    """
                }
            }
        }
    }

    post {
        always {
            cleanWs()
        }
    }
}
