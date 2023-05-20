## up: starts all containers in the background without forcing build
up:
	@echo "Starting persys Docker Services..."
	docker-compose -f ../persys-devops/IaC/docker/docker-compose.yml up -d
	@echo "Docker services started!"


## down: stop docker compose
down:
	@echo "Stopping persys services..."
	docker-compose -f ../persys-devops/IaC/docker/docker-compose.yml down
	@echo "Done!"
