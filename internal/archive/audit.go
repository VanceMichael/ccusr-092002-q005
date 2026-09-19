package archive

import "github.com/vancemichael/092002-retrofit-energy-proof/internal/store"

// VerifyChain 重新校验整条哈希链，任何历史篡改都会返回错误。
func (s *Service) VerifyChain() error { return s.ledger.Verify() }

// ChainHead 返回当前链尾序号与哈希。
func (s *Service) ChainHead() (int64, string) { return s.ledger.Head() }

// ChainEvents 返回审计用的事件清单（载荷完整，供外部核验）。
func (s *Service) ChainEvents() []store.Event { return s.ledger.Events() }
