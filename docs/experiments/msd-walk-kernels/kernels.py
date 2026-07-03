"""Build item-item walk kernels from the MSD training matrix.

Kernels (all top-K pruned per row, K=1000, stored as CSR float32):
  - W_p3_a{alpha}: P3-alpha projection kernel  W = (P_iu^alpha) @ (P_ui^alpha)
    for alpha in {1.0, 0.7}  (alpha=1.0 is the plain P^3 projection)
  - W_cos: item-item cosine similarity (classic item-kNN)
  - W2, W3: pruned powers of the row-normalized alpha=1 kernel, used by the
    truncated-PPR ("direct reader") scorer:  S_k = xhat @ Wk
"""

import os
import sys
import time

import numpy as np
import scipy.sparse as sp

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "out")
TOPK = 1000
BLOCK = 1024


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def prune_topk_rows(mat_csr, k):
    """Keep the k largest-value entries of each CSR row."""
    indptr, indices, data = mat_csr.indptr, mat_csr.indices, mat_csr.data
    new_indptr = [0]
    keep_idx = []
    for i in range(mat_csr.shape[0]):
        s, e = indptr[i], indptr[i + 1]
        if e - s > k:
            part = np.argpartition(data[s:e], -(k))[-k:]
            keep_idx.append(s + part)
            new_indptr.append(new_indptr[-1] + k)
        else:
            keep_idx.append(np.arange(s, e))
            new_indptr.append(new_indptr[-1] + (e - s))
    keep = np.concatenate(keep_idx) if keep_idx else np.empty(0, dtype=np.int64)
    return sp.csr_matrix(
        (data[keep], indices[keep], np.asarray(new_indptr)),
        shape=mat_csr.shape,
    )


def blocked_matmul_topk(A, B, k, block=BLOCK, tag=""):
    """prune_topk(A @ B) computed in row blocks of A to bound memory."""
    n = A.shape[0]
    parts = []
    t0 = time.time()
    for lo in range(0, n, block):
        hi = min(lo + block, n)
        prod = (A[lo:hi] @ B).tocsr()
        prod.data = prod.data.astype(np.float32, copy=False)
        parts.append(prune_topk_rows(prod, k))
        if (lo // block) % 8 == 0:
            log(f"  {tag} rows {hi}/{n} ({time.time()-t0:.0f}s)")
    out = sp.vstack(parts, format="csr")
    log(f"  {tag} done: nnz={out.nnz:,} ({time.time()-t0:.0f}s)")
    return out


def row_normalize(mat):
    mat = mat.tocsr().astype(np.float32)
    rs = np.asarray(mat.sum(axis=1)).ravel()
    rs[rs == 0] = 1.0
    d = sp.diags(1.0 / rs).astype(np.float32)
    return (d @ mat).tocsr()


def main():
    z = np.load(os.path.join(OUT, "split.npz"))
    n_items = int(z["n_items"])
    n_train_users = int(z["n_train_users"])
    uid, sid = z["train_uid"], z["train_sid"]
    log(f"train matrix: {n_train_users:,} x {n_items:,}, nnz {len(uid):,}")

    X = sp.csr_matrix(
        (np.ones(len(uid), dtype=np.float32), (uid, sid)),
        shape=(n_train_users, n_items),
    )
    pop = np.asarray(X.sum(axis=0)).ravel()  # item popularity (train)
    np.save(os.path.join(OUT, "pop.npy"), pop)

    deg_u = np.asarray(X.sum(axis=1)).ravel()
    deg_i = pop.copy()
    deg_u[deg_u == 0] = 1.0
    deg_i[deg_i == 0] = 1.0

    for alpha in (1.0, 0.7):
        P_ui = sp.diags(1.0 / deg_u).astype(np.float32) @ X          # user -> item
        P_iu = (sp.diags(1.0 / deg_i).astype(np.float32) @ X.T.tocsr())  # item -> user
        P_ui, P_iu = P_ui.tocsr(), P_iu.tocsr()
        if alpha != 1.0:
            P_ui.data = np.power(P_ui.data, alpha, dtype=np.float32)
            P_iu.data = np.power(P_iu.data, alpha, dtype=np.float32)
        log(f"building W_p3 alpha={alpha} ...")
        W = blocked_matmul_topk(P_iu, P_ui, TOPK, tag=f"p3a{alpha}")
        sp.save_npz(os.path.join(OUT, f"W_p3_a{alpha}.npz"), W)
        del P_ui, P_iu, W

    # cosine item-kNN
    log("building W_cos ...")
    norms = np.sqrt(pop)
    norms[norms == 0] = 1.0
    Xn = (X @ sp.diags(1.0 / norms).astype(np.float32)).tocsr()
    W_cos = blocked_matmul_topk(Xn.T.tocsr(), Xn, TOPK, tag="cos")
    W_cos.setdiag(0)
    W_cos.eliminate_zeros()
    sp.save_npz(os.path.join(OUT, "W_cos.npz"), W_cos)
    del Xn, W_cos

    # pruned powers of the row-normalized alpha=1 kernel for the PPR reader
    W1 = row_normalize(sp.load_npz(os.path.join(OUT, "W_p3_a1.0.npz")))
    W1.setdiag(0)
    W1 = row_normalize(W1)
    sp.save_npz(os.path.join(OUT, "P1.npz"), W1)
    log("building W2 = prune(P1 @ P1) ...")
    W2 = blocked_matmul_topk(W1, W1, TOPK, tag="W2")
    sp.save_npz(os.path.join(OUT, "P2.npz"), W2)
    log("building W3 = prune(W2 @ P1) ...")
    W3 = blocked_matmul_topk(W2, W1, TOPK, tag="W3")
    sp.save_npz(os.path.join(OUT, "P3.npz"), W3)
    log("all kernels saved")


if __name__ == "__main__":
    sys.exit(main())
