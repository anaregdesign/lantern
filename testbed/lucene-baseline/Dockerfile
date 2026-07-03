# Offline Lucene baseline runner for the search-relevance harness (#887).
# Build + run via run.sh; the image never ships and CI never runs a JVM.
FROM maven:3.9-eclipse-temurin-17 AS build
WORKDIR /app
COPY pom.xml .
RUN mvn -q -B dependency:go-offline
COPY src ./src
RUN mvn -q -B package

FROM eclipse-temurin:17-jre
COPY --from=build /app/target/lucene-baseline-0.1.0.jar /app/runner.jar
ENTRYPOINT ["java", "-jar", "/app/runner.jar"]
