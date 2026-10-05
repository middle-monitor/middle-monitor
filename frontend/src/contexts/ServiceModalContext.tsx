import { createContext, useContext, useState, ReactNode } from 'react';
import { Service, Host } from '../api';

interface ServiceModalContextType {
  isOpen: boolean;
  editingService: Service | Partial<Service> | null;
  hosts: Host[];
  openModal: (
    service?: Service | Partial<Service> | null,
    hosts?: Host[]
  ) => void;
  closeModal: () => void;
  setHosts: (hosts: Host[]) => void;
}

const ServiceModalContext = createContext<ServiceModalContextType | undefined>(
  undefined
);

export function ServiceModalProvider({ children }: { children: ReactNode }) {
  const [isOpen, setIsOpen] = useState(false);
  const [editingService, setEditingService] = useState<
    Service | Partial<Service> | null
  >(null);
  const [hosts, setHosts] = useState<Host[]>([]);

  const openModal = (
    service?: Service | Partial<Service> | null,
    hostsList?: Host[]
  ) => {
    setEditingService(service || null);
    if (hostsList) {
      setHosts(hostsList);
    }
    setIsOpen(true);
  };

  const closeModal = () => {
    setIsOpen(false);
    setEditingService(null);
  };

  return (
    <ServiceModalContext.Provider
      value={{
        isOpen,
        editingService,
        hosts,
        openModal,
        closeModal,
        setHosts,
      }}>
      {children}
    </ServiceModalContext.Provider>
  );
}

export function useServiceModal() {
  const context = useContext(ServiceModalContext);
  if (context === undefined) {
    throw new Error(
      'useServiceModal must be used within a ServiceModalProvider'
    );
  }
  return context;
}




