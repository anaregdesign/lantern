/* Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0. */
/* One open-file-description lock per owner. No PID tests, leases, unlock on
 * isolate/engine detach, finalizers, or exported file descriptors. */
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <unistd.h>

#define API __attribute__((visibility("default")))
#define CAPACITY 16
/* Content-free results: invalid path, conflict, capacity, I/O, stale guard. */
enum { INVALID = 1, CONFLICT = 2, CAP = 3, IO = 4, STALE = 5 };
typedef struct {
  uint64_t id;
  int fd;
  char path[PATH_MAX];
  dev_t db_dev, lock_dev;
  ino_t db_ino, lock_ino;
  int db_exists;
} guard;
static guard guards[CAPACITY];
static uint64_t next_id = 1;
static pthread_mutex_t mutex = PTHREAD_MUTEX_INITIALIZER;

static int regular(const struct stat *s) {
  return S_ISREG(s->st_mode) && s->st_nlink == 1;
}

static int canonical(const char *input, char *output) {
  if (!input || !*input || strlen(input) >= PATH_MAX) return INVALID;
  struct stat s;
  if (stat(input, &s) == 0) {
    if (!regular(&s) || !realpath(input, output)) return INVALID;
    return 0;
  }
  if (errno != ENOENT) return IO;
  /* A dangling DB symlink must not become a separate owner path. */
  if (lstat(input, &s) == 0 || errno != ENOENT) return INVALID;
  char parent[PATH_MAX], resolved[PATH_MAX];
  strcpy(parent, input);
  char *slash = strrchr(parent, '/');
  const char *base = slash ? slash + 1 : input;
  if (!*base || !strcmp(base, ".") || !strcmp(base, "..")) return INVALID;
  char name[PATH_MAX];
  strcpy(name, base);
  if (slash == parent) slash[1] = 0;
  else if (slash) *slash = 0;
  else strcpy(parent, ".");
  if (!realpath(parent, resolved)) return INVALID;
  if (snprintf(output, PATH_MAX, "%s/%s", resolved, name) >= PATH_MAX)
    return INVALID;
  return 0;
}

API int lantern_owner_resolve(const char *path, char *output, int size) {
  if (size < PATH_MAX) return INVALID;
  int result = canonical(path, output);
  if (result) return result;
  /* Some case-insensitive filesystems preserve caller spelling in realpath.
   * Match the actual sidecar identity back to its pinned canonical owner name
   * before the same-isolate factory compares registry keys. */
  char sidecar[PATH_MAX];
  if (snprintf(sidecar, PATH_MAX, "%s.lantern-owner", output) >= PATH_MAX)
    return INVALID;
  struct stat s;
  if (lstat(sidecar, &s)) return errno == ENOENT ? 0 : IO;
  if (!regular(&s)) return INVALID;
  pthread_mutex_lock(&mutex);
  for (int i = 0; i < CAPACITY; i++) if (guards[i].id &&
      guards[i].lock_dev == s.st_dev && guards[i].lock_ino == s.st_ino) {
    strcpy(output, guards[i].path);
    break;
  }
  pthread_mutex_unlock(&mutex);
  return 0;
}

static int validate(guard *g, int require_db) {
  char sidecar[PATH_MAX];
  if (snprintf(sidecar, PATH_MAX, "%s.lantern-owner", g->path) >= PATH_MAX)
    return INVALID;
  struct stat s;
  if (lstat(sidecar, &s) || !regular(&s) || s.st_dev != g->lock_dev ||
      s.st_ino != g->lock_ino) return STALE;
  if (stat(g->path, &s)) return errno == ENOENT && !require_db && !g->db_exists
      ? 0 : STALE;
  if (!regular(&s)) return INVALID;
  if (g->db_exists && (s.st_dev != g->db_dev || s.st_ino != g->db_ino))
    return STALE;
  g->db_dev = s.st_dev;
  g->db_ino = s.st_ino;
  g->db_exists = 1;
  return 0;
}

API int lantern_owner_acquire(const char *path, uint64_t *handle) {
  guard candidate = {0};
  int result = canonical(path, candidate.path);
  if (result) return result;
  char sidecar[PATH_MAX];
  if (snprintf(sidecar, PATH_MAX, "%s.lantern-owner", candidate.path) >= PATH_MAX)
    return INVALID;
  struct stat s;
  if (stat(candidate.path, &s) == 0) {
    candidate.db_exists = 1;
    candidate.db_dev = s.st_dev;
    candidate.db_ino = s.st_ino;
  }
  pthread_mutex_lock(&mutex);
  int slot = -1;
  for (int i = 0; i < CAPACITY; i++) if (!guards[i].id) { slot = i; break; }
  if (slot < 0 || next_id == UINT64_MAX) { result = CAP; goto done; }
  candidate.fd = open(sidecar, O_RDWR | O_CREAT | O_CLOEXEC | O_NOFOLLOW, 0600);
  if (candidate.fd < 0) { result = IO; goto done; }
  if (fstat(candidate.fd, &s) || !regular(&s)) {
    result = INVALID; goto close_candidate;
  }
  candidate.lock_dev = s.st_dev;
  candidate.lock_ino = s.st_ino;
  if (flock(candidate.fd, LOCK_EX | LOCK_NB)) {
    result = errno == EWOULDBLOCK || errno == EAGAIN ? CONFLICT : IO;
    goto close_candidate;
  }
  result = validate(&candidate, 0);
  if (result) goto close_candidate;
  candidate.id = next_id++;
  guards[slot] = candidate;
  *handle = candidate.id;
  goto done;
close_candidate:
  /* Never retry an uncertain close on a potentially recycled numeric FD. */
  (void)close(candidate.fd);
done:
  pthread_mutex_unlock(&mutex);
  return result;
}

API int lantern_owner_check(uint64_t handle, int require_db) {
  int result = STALE;
  pthread_mutex_lock(&mutex);
  for (int i = 0; i < CAPACITY; i++) if (guards[i].id == handle) {
    result = validate(&guards[i], require_db); break;
  }
  pthread_mutex_unlock(&mutex);
  return result;
}

API int lantern_owner_release(uint64_t handle) {
  int result = STALE;
  pthread_mutex_lock(&mutex);
  for (int i = 0; i < CAPACITY; i++) if (guards[i].id == handle) {
    int fd = guards[i].fd;
    guards[i].id = 0;
    result = close(fd) == 0 ? 0 : IO;
    break;
  }
  pthread_mutex_unlock(&mutex);
  return result;
}
