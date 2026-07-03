# MSD replication: graph readers under the Mult-VAE / EASE protocol

Supporting experiment for
[docs/music-recommendation-walk-kernels.md](../../music-recommendation-walk-kernels.md) §8.1.
It reproduces the strong-generalization evaluation protocol of Liang et al.
(WWW 2018, "Variational autoencoders for collaborative filtering") on the
Million Song Dataset taste-profile subset — the same split and metrics used
by Steck (WWW 2019, EASE) — and evaluates the *direct* (graph) readers of the
paper on it: P³, RP³β, item-kNN cosine, and a β-discounted truncated-PPR
mixture. Published numbers for Mult-VAE, Mult-DAE, WMF, CDAE, EASE and the
popularity baseline on this split come from those two papers.

These scripts are analysis artifacts, not part of any Lantern build; they are
plain Python (numpy/scipy/pandas) and touch nothing in the Go workspace.

## Protocol (faithful to dawenl/vae_cf)

- Data: Echo Nest Taste Profile `train_triplets.txt` (48,373,586 triplets).
- Filter: songs with ≥200 listeners first, then users with ≥20 songs
  → 571,355 users × 41,140 items, 33.6M interactions, 0.14% sparsity
  (matches Table 1 of Liang et al. exactly).
- Split: users shuffled with `np.random.seed(98765)`; last 100,000 users held
  out (50,000 validation + 50,000 test); training matrix from the remaining
  471,355 users only (strong generalization).
- Fold-in: per heldout user, 80% of items (seed 98765) form the query
  profile; metrics are computed on the remaining 20%, with fold-in items
  excluded from the ranking.
- Metrics: Recall@20, Recall@50 (normalized by `min(K, |heldout|)`) and
  NDCG@100, averaged over heldout users.

Calibration: the popularity baseline must reproduce the published
0.043 / 0.068 / 0.058 before any other number is trusted.

## Running

```bash
python3 -m venv env && ./env/bin/pip install numpy scipy pandas
curl -LO http://millionsongdataset.com/sites/default/files/challenge/train_triplets.txt.zip
./env/bin/python preprocess.py     # split.npz          (~5 min, ~10 GB RAM)
./env/bin/python kernels.py        # item-item kernels  (~5 min)
./env/bin/python evaluate.py val     # hyperparameter grid on validation users
./env/bin/python evaluate.py valppr  # discounted-PPR grid on validation users
# write out/test_configs.json with the per-family validation winners, then
./env/bin/python evaluate.py test
```

All hyperparameters are selected on the validation users only; test users are
scored once, with the selected configurations.

## Scorers

With `X` the binary train matrix, `P_ui`/`P_iu` its row-normalized user→item /
item→user transition matrices, and `pop` the item popularity vector:

- `p3_a{α}_k{K}`: `W = (P_iu^∘α)(P_ui^∘α)` top-K pruned per row;
  score = normalized fold-in profile · `W` (the P³_α projection kernel).
- `rp3_a{α}_b{β}`: same `W` with columns scaled by `pop^-β` (RP³β).
- `cos_k{K}`: item-item cosine, binary profile (item-kNN).
- `ppr_a{a}_b{β}`: truncated PPR on the row-normalized projection kernel
  `P₁` — score = `Σ_{k≤3} a(1-a)^{k-1} · x̂P₁ᵏ` (powers top-K pruned), columns
  scaled by `pop^-β`. This is the paper's "direct reader" (§4) with the §5
  hub discount.
- `popularity`: calibration row.
