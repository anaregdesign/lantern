"""Evaluate graph readers on the Liang et al. MSD split.

Protocol identical to Mult-VAE / EASE papers: for each heldout user, score all
items from the 80% fold-in, exclude fold-in items, rank, and compute
Recall@20, Recall@50, NDCG@100 over the 20% heldout items.

Families evaluated (hyperparameters tuned on the validation users only):
  popularity                      (calibration against published 0.043/0.068/0.058)
  item-kNN cosine                 topK in {200, 500, 1000}
  P3 (alpha in {1.0, 0.7})        topK in {200, 500, 1000}
  RP3beta                         beta in {0.2,...,0.8} applied as pop^-beta column scaling
  truncated PPR (direct reader)   restart a in {0.3, 0.5, 0.7}: sum_k a(1-a)^{k-1} S_k, k<=3

Usage: evaluate.py val   -> grid over validation users, writes val_results.json
       evaluate.py test  -> evaluates configs in test_configs.json on test users
"""

import json
import os
import sys
import time

import numpy as np
import scipy.sparse as sp

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "out")
CHUNK = 2000
TOPKS = (200, 500, 1000)
BETAS = (0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8)
RESTARTS = (0.3, 0.5, 0.7)


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def load_split(which):
    z = np.load(os.path.join(OUT, "split.npz"))
    n_items = int(z["n_items"])
    pre = "vad" if which.startswith("val") else "test"
    tr_u, tr_s = z[f"{pre}_tr_uid"], z[f"{pre}_tr_sid"]
    te_u, te_s = z[f"{pre}_te_uid"], z[f"{pre}_te_sid"]
    # heldout user ids are global; remap to 0..n-1 over users present in fold-in
    uids = np.unique(tr_u)
    remap = {u: i for i, u in enumerate(uids)}
    tr_u = np.fromiter((remap[u] for u in tr_u), dtype=np.int64, count=len(tr_u))
    te_mask = np.isin(te_u, uids)
    te_u, te_s = te_u[te_mask], te_s[te_mask]
    te_u = np.fromiter((remap[u] for u in te_u), dtype=np.int64, count=len(te_u))
    n_u = len(uids)
    Xtr = sp.csr_matrix(
        (np.ones(len(tr_u), dtype=np.float32), (tr_u, tr_s)), shape=(n_u, n_items)
    )
    Xte = sp.csr_matrix(
        (np.ones(len(te_u), dtype=np.float32), (te_u, te_s)), shape=(n_u, n_items)
    )
    has_te = np.asarray(Xte.sum(axis=1)).ravel() > 0
    log(f"{which}: {n_u:,} fold-in users, {int(has_te.sum()):,} with heldout items")
    return Xtr, Xte, has_te, n_items


def metrics_chunk(S, Xtr_c, Xte_c, sums):
    """Accumulate recall@20/50 and ndcg@100 for a dense score chunk."""
    # exclude fold-in items
    r, c = Xtr_c.nonzero()
    S[r, c] = -np.inf
    n = S.shape[0]
    top100 = np.argpartition(-S, 100, axis=1)[:, :100]
    rowidx = np.arange(n)[:, None]
    ord100 = np.argsort(-S[rowidx, top100], axis=1)
    top100 = np.take_along_axis(top100, ord100, axis=1)

    te = Xte_c.toarray().astype(bool)
    hits = np.take_along_axis(te, top100, axis=1)  # n x 100 boolean
    n_te = te.sum(axis=1)
    valid = n_te > 0

    for K, key in ((20, "r20"), (50, "r50")):
        rec = hits[:, :K].sum(axis=1) / np.minimum(K, np.maximum(n_te, 1))
        sums[key] += rec[valid].sum()
    disc = 1.0 / np.log2(np.arange(2, 102))
    dcg = (hits * disc).sum(axis=1)
    idcg = np.array([disc[: min(int(m), 100)].sum() if m > 0 else 1.0 for m in n_te])
    sums["n100"] += (dcg / idcg)[valid].sum()
    sums["cnt"] += int(valid.sum())


def evaluate(score_fns, Xtr, Xte, has_te):
    """score_fns: dict name -> callable(chunk_csr, xhat_csr) -> dense scores."""
    sums = {name: {"r20": 0.0, "r50": 0.0, "n100": 0.0, "cnt": 0} for name in score_fns}
    n = Xtr.shape[0]
    deg = np.asarray(Xtr.sum(axis=1)).ravel()
    deg[deg == 0] = 1.0
    for lo in range(0, n, CHUNK):
        hi = min(lo + CHUNK, n)
        Xc = Xtr[lo:hi]
        xhat = sp.diags(1.0 / deg[lo:hi]).astype(np.float32) @ Xc
        Tc = Xte[lo:hi]
        for name, fn in score_fns.items():
            S = fn(Xc, xhat)
            metrics_chunk(S, Xc, Tc, sums[name])
        if lo % (CHUNK * 5) == 0:
            log(f"  users {hi}/{n}")
    out = {}
    for name, s in sums.items():
        c = s["cnt"]
        out[name] = {
            "recall@20": round(s["r20"] / c, 5),
            "recall@50": round(s["r50"] / c, 5),
            "ndcg@100": round(s["n100"] / c, 5),
            "users": c,
        }
    return out


def prune_topk(W, k):
    if k >= 1000:
        return W
    W = W.tocsr()
    indptr, indices, data = W.indptr, W.indices, W.data
    keep = []
    new_indptr = [0]
    for i in range(W.shape[0]):
        s, e = indptr[i], indptr[i + 1]
        if e - s > k:
            part = np.argpartition(data[s:e], -k)[-k:]
            keep.append(s + part)
            new_indptr.append(new_indptr[-1] + k)
        else:
            keep.append(np.arange(s, e))
            new_indptr.append(new_indptr[-1] + (e - s))
    keep = np.concatenate(keep)
    return sp.csr_matrix((data[keep], indices[keep], np.asarray(new_indptr)), W.shape)


def main():
    which = sys.argv[1]
    Xtr, Xte, has_te, n_items = load_split(which)
    pop = np.load(os.path.join(OUT, "pop.npy")).astype(np.float32)

    fns = {}

    # popularity (same score for every user)
    pop_row = pop[None, :]
    fns["popularity"] = lambda Xc, xh: np.repeat(pop_row, Xc.shape[0], axis=0).copy()

    if which == "val":
        configs = []
        for a in ("1.0", "0.7"):
            W = sp.load_npz(os.path.join(OUT, f"W_p3_a{a}.npz"))
            for k in TOPKS:
                configs.append((f"p3_a{a}_k{k}", prune_topk(W, k), None))
            for b in BETAS:
                configs.append((f"rp3_a{a}_b{b}_k1000", W, b))
        Wc = sp.load_npz(os.path.join(OUT, "W_cos.npz"))
        for k in TOPKS:
            configs.append((f"cos_k{k}", prune_topk(Wc, k), ("cos", None)))
        for name, W, extra in configs:
            if isinstance(extra, tuple):  # cosine: binary profile
                fns[name] = (lambda W=W: (lambda Xc, xh: (Xc @ W).toarray()))()
            elif extra is None:
                fns[name] = (lambda W=W: (lambda Xc, xh: (xh @ W).toarray()))()
            else:  # rp3 beta column scaling
                scale = np.power(np.maximum(pop, 1.0), -extra).astype(np.float32)
                Wb = (W @ sp.diags(scale)).tocsr()
                fns[name] = (lambda W=Wb: (lambda Xc, xh: (xh @ W).toarray()))()
        # truncated PPR over pruned powers
        P1 = sp.load_npz(os.path.join(OUT, "P1.npz"))
        P2 = sp.load_npz(os.path.join(OUT, "P2.npz"))
        P3 = sp.load_npz(os.path.join(OUT, "P3.npz"))
        for a in RESTARTS:
            c1, c2, c3 = a, a * (1 - a), a * (1 - a) ** 2

            def ppr(Xc, xh, c1=c1, c2=c2, c3=c3):
                s1 = (xh @ P1).toarray()
                s2 = (xh @ P2).toarray()
                s3 = (xh @ P3).toarray()
                return c1 * s1 + c2 * s2 + c3 * s3

            fns[f"ppr_a{a}"] = ppr
    elif which == "valppr":
        # second validation pass: PPR mixture WITH the beta popularity
        # discount, sharing S1/S2/S3 across all configs per chunk
        P1 = sp.load_npz(os.path.join(OUT, "P1.npz"))
        P2 = sp.load_npz(os.path.join(OUT, "P2.npz"))
        P3 = sp.load_npz(os.path.join(OUT, "P3.npz"))
        for a in (0.6, 0.7, 0.8, 0.9):
            for b in (0.3, 0.4, 0.5, 0.6):
                c1, c2, c3 = a, a * (1 - a), a * (1 - a) ** 2
                scale = np.power(np.maximum(pop, 1.0), -b).astype(np.float32)

                def ppr(Xc, xh, c1=c1, c2=c2, c3=c3, scale=scale):
                    s = c1 * (xh @ P1).toarray()
                    s += c2 * (xh @ P2).toarray()
                    s += c3 * (xh @ P3).toarray()
                    return s * scale[None, :]

                fns[f"ppr_a{a}_b{b}"] = ppr
    else:
        with open(os.path.join(OUT, "test_configs.json")) as f:
            chosen = json.load(f)
        for name in chosen:
            if name.startswith("p3_"):
                a = name.split("_")[1][1:]
                k = int(name.split("_k")[1])
                W = prune_topk(sp.load_npz(os.path.join(OUT, f"W_p3_a{a}.npz")), k)
                fns[name] = (lambda W=W: (lambda Xc, xh: (xh @ W).toarray()))()
            elif name.startswith("rp3_"):
                a = name.split("_")[1][1:]
                b = float(name.split("_b")[1].split("_")[0])
                W = sp.load_npz(os.path.join(OUT, f"W_p3_a{a}.npz"))
                scale = np.power(np.maximum(pop, 1.0), -b).astype(np.float32)
                W = (W @ sp.diags(scale)).tocsr()
                fns[name] = (lambda W=W: (lambda Xc, xh: (xh @ W).toarray()))()
            elif name.startswith("cos_"):
                k = int(name.split("_k")[1])
                W = prune_topk(sp.load_npz(os.path.join(OUT, "W_cos.npz")), k)
                fns[name] = (lambda W=W: (lambda Xc, xh: (Xc @ W).toarray()))()
            elif name.startswith("ppr_"):
                rest = name.split("_a")[1]          # e.g. "0.8_b0.4" or "0.7"
                a = float(rest.split("_b")[0])
                b = float(rest.split("_b")[1]) if "_b" in rest else 0.0
                P1 = sp.load_npz(os.path.join(OUT, "P1.npz"))
                P2 = sp.load_npz(os.path.join(OUT, "P2.npz"))
                P3 = sp.load_npz(os.path.join(OUT, "P3.npz"))
                c1, c2, c3 = a, a * (1 - a), a * (1 - a) ** 2
                scale = np.power(np.maximum(pop, 1.0), -b).astype(np.float32)
                fns[name] = (
                    lambda P1=P1, P2=P2, P3=P3, c1=c1, c2=c2, c3=c3, scale=scale: (
                        lambda Xc, xh: (
                            c1 * (xh @ P1).toarray()
                            + c2 * (xh @ P2).toarray()
                            + c3 * (xh @ P3).toarray()
                        )
                        * scale[None, :]
                    )
                )()

    log(f"evaluating {len(fns)} scorers on {which}")
    res = evaluate(fns, Xtr, Xte, has_te)
    path = os.path.join(OUT, f"{which}_results.json")
    with open(path, "w") as f:
        json.dump(res, f, indent=2, sort_keys=True)
    log(f"wrote {path}")
    for name in sorted(res, key=lambda n: -res[n]["ndcg@100"]):
        r = res[name]
        log(f"  {name:24s} r20={r['recall@20']:.4f} r50={r['recall@50']:.4f} n100={r['ndcg@100']:.4f}")


if __name__ == "__main__":
    main()
