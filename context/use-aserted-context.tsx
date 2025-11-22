import React, { useContext } from 'react';

export function useAssertedContext<T>(context: React.Context<T>) {
  const contextValue = useContext(context);
  if (!contextValue) {
    throw new Error('Context not found');
  }

  return contextValue;
}
